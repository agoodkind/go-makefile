package selfupdate

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const (
	// ModeCheck makes the scheduler record release availability only.
	ModeCheck = "check"
	// ModeApply makes the scheduler apply a release when one is available.
	ModeApply = "apply"

	updateDisabledPollInterval = time.Minute
	updateInitialDelay         = time.Minute
)

// SchedulerHooks supplies hot-reloadable scheduler behavior.
type SchedulerHooks struct {
	Enabled         func() bool
	Mode            func() string
	Options         func() Options
	StopForRelaunch func()
	Log             *slog.Logger
}

// RunScheduler runs the daemon-owned update loop until ctx is canceled.
func RunScheduler(ctx context.Context, hooks SchedulerHooks) {
	log := hooks.Log
	if log == nil {
		log = slog.Default()
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			log.ErrorContext(ctx, "update scheduler panic", "err", recovered)
		}
	}()
	runSchedulerLoop(ctx, hooks, log)
}

func runSchedulerLoop(ctx context.Context, hooks SchedulerHooks, log *slog.Logger) {
	for {
		options := schedulerOptions(hooks)
		delay := nextUpdateDelay(schedulerEnabled(hooks), options)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		stop := func() (stop bool) {
			defer func() {
				if recovered := recover(); recovered != nil {
					log.ErrorContext(ctx, "scheduled update panic", "err", recovered)
					stop = false
				}
			}()
			if err := runScheduledUpdate(ctx, hooks, log); err != nil {
				log.WarnContext(ctx, "scheduled update failed", "err", err)
				return false
			}
			state, err := LoadState(resolveOptions(schedulerOptions(hooks)).StatePath)
			if err == nil && state.LastResult == "applied" {
				schedulerStopForRelaunch(hooks)
				return true
			}
			return false
		}()
		if stop {
			return
		}
	}
}

func nextUpdateDelay(enabled bool, options Options) time.Duration {
	if !enabled {
		return updateDisabledPollInterval
	}
	resolvedOptions := resolveOptions(options)
	state, err := LoadState(resolvedOptions.StatePath)
	if err == nil && !state.NextCheckAt.IsZero() {
		delay := until(state.NextCheckAt)
		if delay > 0 {
			return delay
		}
	}
	return jitterDuration(updateInitialDelay)
}

func runScheduledUpdate(ctx context.Context, hooks SchedulerHooks, log *slog.Logger) error {
	if !schedulerEnabled(hooks) {
		return nil
	}
	options := schedulerOptions(hooks)
	options.Log = log.With(slog.String("component", "update"))
	switch schedulerMode(hooks) {
	case ModeCheck:
		_, err := Check(ctx, options)
		if err != nil {
			log.WarnContext(ctx, "scheduled update check failed", "err", err)
			return fmt.Errorf("scheduled update check: %w", err)
		}
		return nil
	case ModeApply:
		if options.Config.CurrentVersion == "dev" || options.Config.CurrentVersion == "unknown" {
			log.DebugContext(
				ctx,
				"scheduled update apply skipped for development build",
				"version",
				options.Config.CurrentVersion,
				"commit",
				options.Config.CurrentCommit,
			)
			_, err := Check(ctx, options)
			if err != nil {
				log.WarnContext(ctx, "scheduled update fallback check failed", "err", err)
				return fmt.Errorf("scheduled update fallback check: %w", err)
			}
			return nil
		}
		_, err := Apply(ctx, options)
		if err != nil {
			log.WarnContext(ctx, "scheduled update apply failed", "err", err)
			return fmt.Errorf("scheduled update apply: %w", err)
		}
		return nil
	default:
		return nil
	}
}

func jitterDuration(base time.Duration) time.Duration {
	if base <= 0 {
		return base
	}
	maxJitter := int64(base / 10)
	if maxJitter <= 0 {
		return base
	}
	offset := timeNow().UnixNano() % maxJitter
	return base + time.Duration(offset)
}

func schedulerEnabled(hooks SchedulerHooks) bool {
	if hooks.Enabled == nil {
		return false
	}
	return hooks.Enabled()
}

func schedulerMode(hooks SchedulerHooks) string {
	if hooks.Mode == nil {
		return ""
	}
	return hooks.Mode()
}

func schedulerOptions(hooks SchedulerHooks) Options {
	if hooks.Options == nil {
		return Options{}
	}
	return hooks.Options()
}

func schedulerStopForRelaunch(hooks SchedulerHooks) {
	if hooks.StopForRelaunch != nil {
		hooks.StopForRelaunch()
	}
}
