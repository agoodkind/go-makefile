package main_test

import (
	"strings"
	"testing"
)

func TestCommandEntryPermitsErrorAndExitHandling(t *testing.T) {
	t.Parallel()
	binary := buildAnalyzer(t)

	for _, scenario := range []struct {
		name   string
		source string
	}{
		{"error wrapping", `package main
import ("fmt"; "log/slog"; "os"; "example.com/fixture/app")
func run() error {
	if err := app.Run(os.Args[1:]); err != nil {
		return fmt.Errorf("run: %w", err)
	}
	return nil
}
func main() {
	slog.Debug("command invoked")
	if err := run(); err != nil {
		slog.Error("command failed", slog.Any("err", err))
		os.Exit(1)
	}
}
`},
		{"error propagation", `package main
import ("log/slog"; "os"; "example.com/fixture/app")
func run(arguments []string) error { return app.Run(arguments) }
func main() {
	slog.Debug("command invoked")
	if err := run(os.Args[1:]); err != nil {
		slog.Error("command failed", slog.Any("err", err))
		os.Exit(1)
	}
}
`},
		{"indexed wrapping and error text", `package main
import ("fmt"; "log/slog"; "os"; "example.com/fixture/app")
func run() error {
	if err := app.Run(os.Args[1:]); err != nil {
		return fmt.Errorf("run: %[1]w", err)
	}
	return nil
}
func main() {
	slog.Debug("command invoked")
	if err := run(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}
`},
		{"status constants", `package main
import ("log/slog"; "os"; "example.com/fixture/app")
const success = 0
const failure = 2
func main() {
	slog.Debug("command invoked")
	status := app.Code(os.Args[1:])
	if status != 0 {
		slog.Error("command failed", slog.Int("status", status))
		os.Exit(failure)
	}
	os.Exit(success)
}
`},
		{"status switch", `package main
import ("log/slog"; "os"; "example.com/fixture/app")
func main() {
	slog.Debug("command invoked")
	switch status := app.Code(os.Args[1:]); status {
	case 0: os.Exit(0)
	default: os.Exit(2)
	}
}
`},
		{"generic delegation", `package main
import ("log/slog"; "os"; "example.com/fixture/app")
func main() {
	slog.Debug("command invoked")
	os.Exit(app.Generic[int](os.Args[1:]))
}
`},
		{"status conversion", `package main
import ("log/slog"; "os"; "example.com/fixture/app")
func main() {
	slog.Debug("command invoked")
	status := app.Code(os.Args[1:])
	os.Exit(int(status))
}
`},
		{"result propagation", `package main
import ("fmt"; "log/slog"; "os"; "example.com/fixture/app")
func run(arguments []string) (int, error) {
	status, err := app.Result(arguments)
	if err != nil {
		return status, fmt.Errorf("run: %w", err)
	}
	return status, nil
}
func main() {
	slog.Debug("command invoked")
	status, err := run(os.Args[1:])
	if err != nil {
		slog.Error("command failed", slog.Any("err", err))
		os.Exit(1)
	}
	os.Exit(status)
}
`},
		{"error identity check", `package main
import ("errors"; "log/slog"; "os"; "example.com/fixture/app")
func main() {
	slog.Debug("command invoked")
	err := app.Run(os.Args[1:])
	if errors.Is(err, app.ErrFailure) {
		slog.Error("command failed", slog.Any("err", err))
		os.Exit(2)
	}
	if !errors.Is(err, nil) {
		os.Exit(1)
	}
}
`},
		{"default status mapping", `package main
import ("log/slog"; "os"; "example.com/fixture/app")
func main() {
	slog.Debug("command invoked")
	status := 0
	if err := app.Run(os.Args[1:]); err != nil {
		status = 2
	}
	os.Exit(status)
}
`},
		{"callback references", `package main
import ("log/slog"; "os"; "example.com/fixture/app")
func run(arguments []string) int { return app.Code(arguments) }
func main() {
	slog.Debug("command invoked")
	os.Exit(app.WithCallback(run, os.Args[1:]))
}
`},
		{"imported callback adapter", `package main
import ("log/slog"; "os"; "example.com/fixture/app")
func main() {
	slog.Debug("command invoked")
	os.Exit(app.WithCallback(func(arguments []string) int { return app.Code(arguments) }, os.Args[1:]))
}
`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := analyzerFixture(t)
			writeEntryApplication(t, fixture)
			writeFixtureFile(t, fixture, "cmd/tool/main.go", scenario.source)
			analyzeFixture(t, binary, fixture, false, "-commandentry", "-missing_boundary_log", "./cmd/tool")
		})
	}
}

func TestCommandEntryUsesImportOwnership(t *testing.T) {
	t.Parallel()
	binary := buildAnalyzer(t)
	fixture := analyzerFixture(t)
	writeFixtureFile(t, fixture, "go.mod", "module example.com/fixture\n\ngo 1.26\n\nrequire corp/cli v0.0.0\nreplace corp/cli => ./library\n")
	writeFixtureFile(t, fixture, "library/go.mod", "module corp/cli\n\ngo 1.26\n")
	writeFixtureFile(t, fixture, "library/run.go", "package cli\nfunc Run() int { return 0 }\n")
	writeFixtureFile(t, fixture, "cmd/tool/main.go", "package main\nimport (\"corp/cli\"; \"log/slog\"; \"os\")\nfunc main() { slog.Debug(\"command invoked\"); os.Exit(cli.Run()) }\n")
	analyzeFixture(t, binary, fixture, false, "-commandentry", "-missing_boundary_log", "./cmd/tool")
}

func TestCommandEntryForwardsExistingErrorSentinel(t *testing.T) {
	t.Parallel()
	binary := buildAnalyzer(t)
	fixture := analyzerFixture(t)
	writeEntryApplication(t, fixture)
	writeFixtureFile(t, fixture, "cmd/tool/main.go", `package main
import ("errors"; "fmt"; "log/slog"; "example.com/fixture/app")
var errNoFiles = app.ErrFailure
func run() (string, []string, error) {
	binary, arguments, err := app.Command(app.Code, errNoFiles)
	if err != nil {
		wrapped := fmt.Errorf("prepare command: %w", err)
		if !errors.Is(err, errNoFiles) {
			slog.Error("prepare command", slog.Any("err", wrapped))
		}
		return "", nil, wrapped
	}
	return binary, arguments, nil
}
func main() { slog.Debug("command invoked"); app.Code(nil) }
`)
	output := analyzeFixture(t, binary, fixture, true, "-commandentry", "-missing_boundary_log", "./cmd/tool")
	if !strings.Contains(output, "errNoFiles contains implementation") || strings.Contains(output, "run contains implementation") {
		t.Fatalf("error forwarding changed the declaration policy or rejected the wrapper:\n%s", output)
	}
}

func TestCommandEntryRejectsImplementation(t *testing.T) {
	t.Parallel()
	binary := buildAnalyzer(t)

	for _, scenario := range []struct {
		name   string
		file   string
		source string
	}{
		{"parsing", "main.go", "package main\nimport parsing \"flag\"\nfunc main() { parsing.Parse() }\n"},
		{"range", "main.go", "package main\nimport \"os\"\nfunc main() { for _, argument := range os.Args { println(argument) } }\n"},
		{"loop", "main.go", "package main\nfunc main() { for { } }\n"},
		{"computation", "main.go", "package main\nfunc business(value int) int { return value + 1 }\nfunc main() {}\n"},
		{"setup", "main.go", "package main\nimport \"net/http\"\nfunc main() { server := http.Server{}; server.ListenAndServe() }\n"},
		{"imported receiver setup", "main.go", "package main\nimport \"example.com/fixture/app\"\nfunc main() { app.Service{}.Run() }\n"},
		{"arbitrary condition", "main.go", "package main\nimport \"example.com/fixture/app\"\nfunc route(condition bool) error { if condition { return app.Run(nil) }; return nil }\nfunc main() {}\n"},
		{"numeric business condition", "main.go", "package main\nimport \"example.com/fixture/app\"\nfunc route(value int) int { if value > 3 { return app.Code(nil) }; return 0 }\nfunc main() {}\n"},
		{"delegated business condition", "main.go", "package main\nimport \"example.com/fixture/app\"\nfunc main() { count := app.Count(); if count > 10 { app.Delete() } }\n"},
		{"business condition before exit", "main.go", "package main\nimport (\"os\"; \"example.com/fixture/app\")\nfunc main() { count := app.Count(); if count > 10 { app.Delete() }; os.Exit(0) }\n"},
		{"unrelated literal setup", "main.go", "package main\nimport \"example.com/fixture/app\"\nfunc main() { value := 42; _ = value; app.Code(nil) }\n"},
		{"callback implementation", "main.go", "package main\nimport (\"os\"; \"example.com/fixture/app\")\nfunc main() { app.WithCallback(func(arguments []string) int { return len(arguments) }, os.Args[1:]) }\n"},
		{"generated implementation", "main_gen.go", "// Code generated by example; DO NOT EDIT.\npackage main\nimport \"os\"\nfunc main() { for _, argument := range os.Args { println(argument) } }\n"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := analyzerFixture(t)
			writeEntryApplication(t, fixture)
			writeFixtureFile(t, fixture, "cmd/tool/"+scenario.file, scenario.source)
			output := analyzeFixture(t, binary, fixture, true, "-commandentry", "./cmd/tool")
			if !strings.Contains(output, "commandentry:") {
				t.Fatalf("command implementation did not produce a commandentry diagnostic:\n%s", output)
			}
		})
	}
}

func TestCommandEntryForwardsPackageScopeValues(t *testing.T) {
	t.Parallel()
	binary := buildAnalyzer(t)
	fixture := analyzerFixture(t)
	writeEntryApplication(t, fixture)
	writeFixtureFile(t, fixture, "app/pattern.go", "package app\nimport \"regexp\"\nfunc AcceptPattern(pattern *regexp.Regexp) {}\n")
	writeFixtureFile(t, fixture, "cmd/tool/main.go", `package main
import ("log/slog"; "regexp"; "example.com/fixture/app")
var goFindingPattern = regexp.MustCompile("finding")
func main() {
	slog.Debug("command invoked")
	app.AcceptPattern(goFindingPattern)
}
`)
	output := analyzeFixture(t, binary, fixture, true, "-commandentry", "-missing_boundary_log", "./cmd/tool")
	if !strings.Contains(output, "goFindingPattern contains implementation") || strings.Contains(output, "main contains implementation") || strings.Count(output, "commandentry:") != 1 {
		t.Fatalf("forwarding a global changed the declaration policy or rejected the entry point:\n%s", output)
	}
}

func TestDelegatingMainRequiresBoundaryLog(t *testing.T) {
	t.Parallel()
	binary := buildAnalyzer(t)
	fixture := analyzerFixture(t)
	writeEntryApplication(t, fixture)
	writeFixtureFile(t, fixture, "cmd/tool/main.go", "package main\nimport \"example.com/fixture/app\"\nfunc main() { app.Code(nil) }\n")
	output := analyzeFixture(t, binary, fixture, true, "-commandentry", "-missing_boundary_log", "./cmd/tool")
	if !strings.Contains(output, "boundary function must emit") || strings.Contains(output, "commandentry:") {
		t.Fatalf("delegation did not retain the boundary logging requirement:\n%s", output)
	}
}

func writeEntryApplication(t *testing.T, fixture string) {
	t.Helper()
	writeFixtureFile(t, fixture, "app/app.go", `package app
import "errors"
var ErrFailure = errors.New("failed")
func Run(arguments []string) error { return nil }
func Code(arguments []string) int { return 0 }
func Generic[T any](arguments []string) int { return 0 }
func Result(arguments []string) (int, error) { return 0, nil }
type Service struct{}
func (Service) Run() {}
func Count() int { return 11 }
func Delete() {}
func WithCallback(run func([]string) int, arguments []string) int { return run(arguments) }
func Command(run func([]string) int, noFiles error) (string, []string, error) { return "", nil, noFiles }
`)
}
