package main_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	commandNotice          = "14"
	filenameNotice         = "15"
	argumentsNotice        = "16"
	whitespaceNotice       = "17"
	fixtureStaticcheckGate = "staticcheck-extra"
	fixtureGolangciGate    = "lint-golangci"
	fixtureTestFlagsOff    = "STATICCHECK_EXTRA_TEST_FLAGS="
	fixtureArgumentFlags   = "STATICCHECK_EXTRA_FLAGS=-argumentlines"
	fixtureCommandFlag     = "-commandentry"
	fixtureArgumentFlag    = "-argumentlines"
	fixtureWidget          = "widget/widget.go"
	fixtureWidgetTest      = "widget/widget_test.go"
	fixtureCommand         = "cmd/tool/main.go"
	fixtureMakefile        = "Makefile"
	fixtureModule          = "go.mod"
	fixtureLinux           = "linux"
	fixtureDarwin          = "darwin"
	fixtureWindows         = "windows"
	fixtureWidgetPackage   = "package widget\n"
)

func setPendingNotices(t *testing.T, consumer string, pending ...string) string {
	t.Helper()
	catalog := readConsumerFile(t, filepath.Join("..", ".."), "notices.txt")
	writeConsumerFile(t, consumer, "notices.txt", catalog)
	var applied strings.Builder
	var found []string

	for line := range strings.SplitSeq(catalog, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		identifier, _, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("invalid notice record: %s", line)
		}

		if slices.Contains(pending, identifier) {
			found = append(found, identifier)

			continue
		}

		applied.WriteString(identifier + "\n")
	}

	if len(found) != len(pending) {
		t.Fatalf("requested pending notices %v, found %v", pending, found)
	}

	previous := applied.String()
	writeConsumerFile(t, consumer, appliedNoticeFile, previous)

	return previous
}

type structureExample struct {
	name       string
	flag       string
	historical map[string]string
	later      map[string]string
	finding    string
}

func structureExamples() []structureExample {
	const sum = fixtureWidgetPackage + "func sum(first, second int) int { return first + second }\n"
	const wrapped = "sum(\nvalue, 2,\n)"
	const short = "sum(value, 2)"
	const double = "func Double(value int) int { return "
	const historical = sum + double + wrapped + " }\n"

	return []structureExample{
		{
			name: "commandentry",
			flag: fixtureCommandFlag,
			historical: map[string]string{
				fixtureCommand: "package main\nfunc main() { println(\"historical\") }\n",
			},
			later: map[string]string{
				fixtureCommand: "package main\nfunc main() { println(\"historical\") }\nfunc helper() {}\n",
			},
			finding: "cmd/tool/main.go:",
		},
		{
			name: "repeated initializer",
			flag: fixtureCommandFlag,
			historical: map[string]string{
				fixtureCommand: "package main\nfunc main() { println(\"historical\") }\nfunc init() {}\n",
			},
			later: map[string]string{
				fixtureCommand: "package main\nfunc main() { println(\"historical\") }\nfunc init() {}\nfunc init() {}\n",
			},
			finding: "cmd/tool/main.go:",
		},
		{
			name: "filenameprefix",
			flag: "-filenameprefix",
			historical: map[string]string{
				"widget/bar_abc.go": fixtureWidgetPackage,
				"widget/barxyz.go":  fixtureWidgetPackage,
			},
			later: map[string]string{
				"widget/bar_later.go": fixtureWidgetPackage,
			},
			finding: "widget/bar_later.go:",
		},
		{
			name: "argumentlines",
			flag: fixtureArgumentFlag,
			historical: map[string]string{
				fixtureWidget: historical,
			},
			later: map[string]string{
				fixtureWidget: historical + "func Later() int { return sum(\n1, 2,\n) }\n",
			},
			finding: fixtureWidget + ":",
		},
		{
			name: "repeated argument",
			flag: fixtureArgumentFlag,
			historical: map[string]string{
				fixtureWidget: historical,
			},
			later: map[string]string{
				fixtureWidget: sum + double + wrapped + " + " + wrapped + " }\n",
			},
			finding: fixtureWidget + ":",
		},
		{
			name: "formatted occurrence",
			flag: fixtureArgumentFlag,
			historical: map[string]string{
				fixtureWidget: sum + double + wrapped + " + " + short + " }\n",
			},
			later: map[string]string{
				fixtureWidget: sum + double + short + " + " + wrapped + " }\n",
			},
			finding: fixtureWidget + ":",
		},
	}
}

// TestStructureNoticesRejectLaterViolations checks each source structure
// notice.
func TestStructureNoticesRejectLaterViolations(t *testing.T) {
	t.Parallel()

	for _, scenario := range structureExamples() {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			consumer := newConsumerRepository(t, beforeNoticeDate)
			pending := map[string]string{
				fixtureCommandFlag:  commandNotice,
				"-filenameprefix":   filenameNotice,
				fixtureArgumentFlag: argumentsNotice,
			}[scenario.flag]
			if pending == "" {
				t.Fatalf("missing notice for %s", scenario.flag)
			}

			previous := setPendingNotices(t, consumer, pending)

			for name, contents := range scenario.historical {
				writeConsumerFile(t, consumer, name, contents)
			}

			runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
			runConsumerGit(t, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add historical source")
			arguments := []string{fixtureStaticcheckGate, "STATICCHECK_EXTRA_FLAGS=" + scenario.flag, fixtureTestFlagsOff}
			output, err := runConsumerMake(consumer, arguments...)
			if err != nil {
				t.Fatalf("historical violations failed after notice application: %v\n%s", err, output)
			}

			if applied := readConsumerFile(t, consumer, appliedNoticeFile); applied != previous+pending+"\n" {
				t.Fatalf("notice %s did not apply:\n%s\n%s", pending, applied, output)
			}

			baseline := readConsumerFile(t, consumer, baselineFileName)
			if !strings.Contains(baseline, strings.TrimPrefix(scenario.flag, "-")+":") {
				t.Fatalf("notice did not baseline the historical finding:\n%s\n%s", baseline, output)
			}

			for name, contents := range scenario.later {
				writeConsumerFile(t, consumer, name, contents)
			}

			output, err = runConsumerMake(consumer, arguments...)
			if err == nil || !strings.Contains(output, scenario.finding) {
				t.Fatalf("gate did not reject the later violation: %v\n%s", err, output)
			}

			if current := readConsumerFile(t, consumer, baselineFileName); current != baseline {
				t.Fatalf("a later gate changed the historical baseline:\n%s", current)
			}
		})
	}
}
