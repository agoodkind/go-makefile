package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStructureNoticesRejectLaterViolations(t *testing.T) {
	t.Parallel()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	notices, err := os.ReadFile(filepath.Join(repository, "notices.txt"))
	if err != nil {
		t.Fatal(err)
	}

	for _, scenario := range []struct {
		name       string
		flag       string
		historical map[string]string
		later      map[string]string
		finding    string
	}{
		{
			name: "commandentry",
			flag: "-commandentry",
			historical: map[string]string{
				"cmd/tool/main.go": "package main\nfunc main() { println(\"historical\") }\n",
			},
			later: map[string]string{
				"cmd/tool/main.go": "package main\nfunc main() { println(\"historical\") }\nfunc helper() {}\n",
			},
			finding: "cmd/tool/main.go:",
		},
		{
			name: "repeated initializer",
			flag: "-commandentry",
			historical: map[string]string{
				"cmd/tool/main.go": "package main\nfunc main() { println(\"historical\") }\nfunc init() {}\n",
			},
			later: map[string]string{
				"cmd/tool/main.go": "package main\nfunc main() { println(\"historical\") }\nfunc init() {}\nfunc init() {}\n",
			},
			finding: "cmd/tool/main.go:",
		},
		{
			name: "filenameprefix",
			flag: "-filenameprefix",
			historical: map[string]string{
				"widget/bar_abc.go": "package widget\n",
				"widget/barxyz.go":  "package widget\n",
			},
			later: map[string]string{
				"widget/bar_later.go": "package widget\n",
			},
			finding: "widget/bar_later.go:",
		},
		{
			name: "argumentlines",
			flag: "-argumentlines",
			historical: map[string]string{
				"widget/widget.go": "package widget\nfunc sum(first, second int) int { return first + second }\nfunc Double(value int) int { return sum(\nvalue, value,\n) }\n",
			},
			later: map[string]string{
				"widget/widget.go": "package widget\nfunc sum(first, second int) int { return first + second }\nfunc Double(value int) int { return sum(\nvalue, value,\n) }\nfunc Later() int { return sum(\n1, 2,\n) }\n",
			},
			finding: "widget/widget.go:",
		},
		{
			name: "repeated argument",
			flag: "-argumentlines",
			historical: map[string]string{
				"widget/widget.go": "package widget\nfunc sum(first, second int) int { return first + second }\nfunc Double(value int) int { return sum(\nvalue, value,\n) }\n",
			},
			later: map[string]string{
				"widget/widget.go": "package widget\nfunc sum(first, second int) int { return first + second }\nfunc Double(value int) int { return sum(\nvalue, value,\n) + sum(\nvalue, value,\n) }\n",
			},
			finding: "widget/widget.go:",
		},
		{
			name: "formatted occurrence",
			flag: "-argumentlines",
			historical: map[string]string{
				"widget/widget.go": "package widget\nfunc sum(first, second int) int { return first + second }\nfunc Double(value int) int { return sum(\nvalue, value,\n) + sum(value, value) }\n",
			},
			later: map[string]string{
				"widget/widget.go": "package widget\nfunc sum(first, second int) int { return first + second }\nfunc Double(value int) int { return sum(value, value) + sum(\nvalue, value,\n) }\n",
			},
			finding: "widget/widget.go:",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			consumer := newConsumerRepository(t, beforeNoticeDate)
			pending := map[string]string{"-commandentry": "3", "-filenameprefix": "4", "-argumentlines": "5"}[scenario.flag]
			if pending == "" {
				t.Fatalf("missing notice for %s", scenario.flag)
			}

			var applied strings.Builder

			for _, id := range []string{"1", "2", "3", "4", "5", "6"} {
				if id != pending {
					applied.WriteString(id + "\n")
				}
			}

			writeConsumerFile(t, consumer, appliedNoticeFile, applied.String())
			writeConsumerFile(t, consumer, "notices.txt", string(notices))

			for name, contents := range scenario.historical {
				writeConsumerFile(t, consumer, name, contents)
			}

			runConsumerGit(t, consumer, beforeNoticeDate, "add", ".")
			runConsumerGit(t, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add historical source")
			arguments := []string{"staticcheck-extra", "STATICCHECK_EXTRA_FLAGS=" + scenario.flag, "STATICCHECK_EXTRA_TEST_FLAGS="}
			output, err := runConsumerMake(consumer, arguments...)
			if err != nil {
				t.Fatalf("historical violations failed after notice application: %v\n%s", err, output)
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
