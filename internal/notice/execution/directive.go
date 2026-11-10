package execution

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"goodkind.io/go-makefile/internal/notice"
	"goodkind.io/go-makefile/internal/notice/history"
)

const makeDirectory = ".make"

const staticcheckGate = "staticcheck-extra"

type record struct {
	id        string
	directive string
	summary   string
}

type directive struct {
	gate       string
	linter     string
	rule       string
	pattern    string
	introduced time.Time
	policy     history.Policy
}

func parseDirective(value string) directive {
	parsed := directive{
		gate:       "golangci",
		linter:     "",
		rule:       "",
		pattern:    "",
		introduced: time.Time{},
		policy:     history.Cutoff,
	}

	for token := range strings.FieldsSeq(value) {
		switch {
		case token == "UPGRADE=1":
			parsed.policy = history.Upgrade
		case strings.HasPrefix(token, "GATE="):
			parsed.gate = strings.TrimPrefix(token, "GATE=")
		case strings.HasPrefix(token, "LINTER="):
			parsed.linter = strings.TrimPrefix(token, "LINTER=")
		case strings.HasPrefix(token, "RULE="):
			parsed.rule = strings.TrimPrefix(token, "RULE=")
		case strings.HasPrefix(token, "PATTERN="):
			parsed.pattern = strings.TrimPrefix(token, "PATTERN=")
		case strings.HasPrefix(token, "INTRODUCED="):
			introduced, err := time.Parse(time.RFC3339, strings.TrimPrefix(token, "INTRODUCED="))
			if err == nil {
				parsed.introduced = introduced
			}
		}
	}

	return parsed
}

func shouldBaseline(ctx context.Context, value directive, paths []string) (bool, error) {
	if value.introduced.IsZero() {
		return BaselinesHaveContent(paths), nil
	}

	adopted, found, err := notice.Adoption(ctx, ".")
	if err != nil {
		wrapped := fmt.Errorf("determine automatic notice eligibility: %w", err)
		slog.ErrorContext(ctx, "notice eligibility failed", slog.Any("error", wrapped))

		return false, wrapped
	}

	if !found {
		return BaselinesHaveContent(paths), nil
	}

	return adopted.Before(value.introduced), nil
}

// BaselinesHaveContent checks the configured files for an existing baseline.
//
// Empty files do not establish historical adoption. An unreadable file keeps
// the conservative historical classification.
//
// A caller supplies the configured file paths:
//
//	historical := BaselinesHaveContent(paths)
func BaselinesHaveContent(paths []string) bool {
	return slices.ContainsFunc(paths, baselineHasContent)
}

func baselineHasContent(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return !os.IsNotExist(err)
	}

	return info.Size() > 0
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}

	return fallback
}

func restoreEnv(name, previous string, had bool) {
	if had {
		_ = os.Setenv(name, previous)

		return
	}

	_ = os.Unsetenv(name)
}
