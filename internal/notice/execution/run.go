package execution

import (
	"context"
	"os"
	"path/filepath"
)

func (runner *Runner) run(ctx context.Context, options settings) int {
	noticesFile := envDefault("_GO_MK_NOTICES_FILE", filepath.Join(makeDirectory, "notices.txt"))
	appliedFile := envDefault("GO_MK_APPLIED_NOTICES", ".go-mk-applied-notices")
	seenFile := filepath.Join(makeDirectory, ".go-mk-notice-seen")
	if _, err := os.Stat(noticesFile); err != nil {
		return 0
	}

	applied, _ := loadApplied(appliedFile)
	lastSeen, _ := loadSeen(seenFile)

	records, err := readRecords(noticesFile)
	if err != nil {
		return 0
	}

	if options.ci {
		reportCI(records, applied)

		return 0
	}

	maximum, complete := runner.process(ctx, options, records, appliedFile, applied, lastSeen)
	if !complete || options.gate != "" {
		return 0
	}

	if err := os.MkdirAll(makeDirectory, 0o755); err != nil {
		_ = fileFailure("create notice directory", err)

		return 0
	}

	_ = writeSeen(seenFile, maximum)

	return 0
}

func (runner *Runner) process(
	ctx context.Context,
	options settings,
	records []record,
	appliedFile string,
	applied map[string]bool,
	lastSeen int,
) (int, bool) {
	maximum := lastSeen

	for _, current := range records {
		numeric, valid := noticeNumber(current.id)
		if !valid {
			continue
		}

		isDirective := current.directive != "" && current.directive != "-"
		var value directive
		automatic := false
		if isDirective {
			value = parseDirective(current.directive)
			var err error
			automatic, err = shouldBaseline(ctx, value, options.baselines)
			if err != nil {
				stderr(
					"go-makefile notice #" + current.id + ": adoption history could not be read. " +
						"Repair the reported Git error and rerun make build.\n",
				)

				return maximum, false
			}
		}

		if options.gate != "" {
			if isDirective && value.gate == options.gate && !applied[current.id] && automatic {
				runner.apply(ctx, options, current, value, appliedFile, applied)
			}

			continue
		}

		if isDirective && !applied[current.id] {
			if automatic {
				runner.apply(ctx, options, current, value, appliedFile, applied)
			} else {
				recordFresh(current, appliedFile, applied)
			}
		}

		if numeric > lastSeen && (!isDirective || automatic || !applied[current.id]) {
			stderr("go-makefile notice #" + current.id + ": " + current.summary + "\n")
		}

		maximum = max(maximum, numeric)
	}

	return maximum, true
}

func reportCI(records []record, applied map[string]bool) {
	for _, current := range records {
		if current.directive == "" || current.directive == "-" || applied[current.id] {
			continue
		}

		stderr(
			"go-makefile notice #" + current.id + " is not applied. Run make check locally, " +
				"then commit the files it changes or fix the findings.\n",
		)
	}
}

func recordFresh(current record, path string, applied map[string]bool) {
	if err := appendApplied(path, current.id); err != nil {
		stderr(
			"go-makefile notice #" + current.id + ": could not record applied notice in " + path +
				"; will retry on the next run\n",
		)

		return
	}

	applied[current.id] = true
}
