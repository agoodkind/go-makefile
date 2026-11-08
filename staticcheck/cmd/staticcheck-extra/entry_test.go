package main_test

import (
	"strings"
	"testing"
)

const entryMainFileName = "main.go"

var entryHandlingCases = []struct {
	name   string
	source string
}{
	{"error wrapping", `package main

import (
	"example.com/fixture/app"
	"fmt"
	"log/slog"
	"os"
)

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

import (
	"example.com/fixture/app"
	"log/slog"
	"os"
)

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

import (
	"example.com/fixture/app"
	"fmt"
	"log/slog"
	"os"
)

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

import (
	"example.com/fixture/app"
	"log/slog"
	"os"
)

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

import (
	"example.com/fixture/app"
	"log/slog"
	"os"
)

func main() {
	slog.Debug("command invoked")
	switch status := app.Code(os.Args[1:]); status {
	case 0:
		os.Exit(0)
	default:
		os.Exit(2)
	}
}
`},
	{"generic delegation", `package main

import (
	"example.com/fixture/app"
	"log/slog"
	"os"
)

func main() {
	slog.Debug("command invoked")
	os.Exit(app.Generic[int](os.Args[1:]))
}
`},
	{"status conversion", `package main

import (
	"example.com/fixture/app"
	"log/slog"
	"os"
)

func main() {
	slog.Debug("command invoked")
	status := app.Code(os.Args[1:])
	os.Exit(int(status))
}
`},
	{"result propagation", `package main

import (
	"example.com/fixture/app"
	"fmt"
	"log/slog"
	"os"
)

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

import (
	"errors"
	"example.com/fixture/app"
	"log/slog"
	"os"
)

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

import (
	"example.com/fixture/app"
	"log/slog"
	"os"
)

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

import (
	"example.com/fixture/app"
	"log/slog"
	"os"
)

func run(arguments []string) int { return app.Code(arguments) }
func main() {
	slog.Debug("command invoked")
	os.Exit(app.WithCallback(run, os.Args[1:]))
}
`},
	{"imported callback adapter", `package main

import (
	"example.com/fixture/app"
	"log/slog"
	"os"
)

func main() {
	slog.Debug("command invoked")
	os.Exit(app.WithCallback(func(arguments []string) int { return app.Code(arguments) }, os.Args[1:]))
}
`},
}

var entryImplementationCases = []struct {
	name   string
	file   string
	source string
}{
	{"parsing", entryMainFileName, `package main

import parsing "flag"

func main() { parsing.Parse() }
`},
	{"range", entryMainFileName, `package main

import "os"

func main() {
	for _, argument := range os.Args {
		println(argument)
	}
}
`},
	{"loop", entryMainFileName, `package main

func main() {
	for {
	}
}
`},
	{"computation", entryMainFileName, `package main

func business(value int) int { return value + 1 }
func main()                  {}
`},
	{"setup", entryMainFileName, `package main

import "net/http"

func main() { server := http.Server{}; server.ListenAndServe() }
`},
	{"imported receiver setup", entryMainFileName, `package main

import "example.com/fixture/app"

func main() { app.Service{}.Run() }
`},
	{"arbitrary condition", entryMainFileName, `package main

import "example.com/fixture/app"

func route(condition bool) error {
	if condition {
		return app.Run(nil)
	}
	return nil
}
func main() {}
`},
	{"numeric business condition", entryMainFileName, `package main

import "example.com/fixture/app"

func route(value int) int {
	if value > 3 {
		return app.Code(nil)
	}
	return 0
}
func main() {}
`},
	{"delegated business condition", entryMainFileName, `package main

import "example.com/fixture/app"

func main() {
	count := app.Count()
	if count > 10 {
		app.Delete()
	}
}
`},
	{"business condition before exit", entryMainFileName, `package main

import (
	"example.com/fixture/app"
	"os"
)

func main() {
	count := app.Count()
	if count > 10 {
		app.Delete()
	}
	os.Exit(0)
}
`},
	{"unrelated literal setup", entryMainFileName, `package main

import "example.com/fixture/app"

func main() { value := 42; _ = value; app.Code(nil) }
`},
	{"callback implementation", entryMainFileName, `package main

import (
	"example.com/fixture/app"
	"os"
)

func main() { app.WithCallback(func(arguments []string) int { return len(arguments) }, os.Args[1:]) }
`},
	{"generated implementation", "main_gen.go", `// Code generated by example; DO NOT EDIT.
package main

import "os"

func main() {
	for _, argument := range os.Args {
		println(argument)
	}
}
`},
}

// TestCommandEntryPermitsErrorAndExitHandling checks thin command error
// handling and process status updates.
func TestCommandEntryPermitsErrorAndExitHandling(t *testing.T) {
	t.Parallel()
	binary := buildAnalyzer(t)

	for _, scenario := range entryHandlingCases {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			fixture := analyzerFixture(t)
			writeEntryApplication(t, fixture)
			writeFixtureFile(t, fixture, "cmd/tool/main.go", scenario.source)
			analyzeFixture(t, binary, fixture, false, "-commandentry", "-missing_boundary_log", "./cmd/tool")
		})
	}
}

// TestCommandEntryUsesImportOwnership checks a local module import.
func TestCommandEntryUsesImportOwnership(t *testing.T) {
	t.Parallel()
	binary := buildAnalyzer(t)
	fixture := analyzerFixture(t)
	writeFixtureFile(t, fixture, "go.mod", `module example.com/fixture

go 1.26

require corp/cli v0.0.0
replace corp/cli => ./library
`)
	writeFixtureFile(t, fixture, "library/go.mod", `module corp/cli

go 1.26
`)
	writeFixtureFile(t, fixture, "library/run.go", `package cli

func Run() int { return 0 }
`)
	writeFixtureFile(t, fixture, "cmd/tool/main.go", `package main

import (
	"corp/cli"
	"log/slog"
	"os"
)

func main() { slog.Debug("command invoked"); os.Exit(cli.Run()) }
`)
	analyzeFixture(t, binary, fixture, false, "-commandentry", "-missing_boundary_log", "./cmd/tool")
}

// TestCommandEntryForwardsExistingErrorSentinel checks error forwarding
// without exempting the sentinel declaration.
func TestCommandEntryForwardsExistingErrorSentinel(t *testing.T) {
	t.Parallel()
	binary := buildAnalyzer(t)
	fixture := analyzerFixture(t)
	writeEntryApplication(t, fixture)
	writeFixtureFile(t, fixture, "cmd/tool/main.go", `package main

import (
	"errors"
	"example.com/fixture/app"
	"fmt"
	"log/slog"
)

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
	hasDeclaration := strings.Contains(output, "errNoFiles contains implementation")
	rejectsWrapper := strings.Contains(output, "run contains implementation")
	if !hasDeclaration || rejectsWrapper {
		t.Fatalf("error forwarding changed the declaration policy or rejected the wrapper:\n%s", output)
	}
}

// TestCommandEntryRejectsImplementation rejects parsing, setup, and
// business operations inside command packages.
func TestCommandEntryRejectsImplementation(t *testing.T) {
	t.Parallel()
	binary := buildAnalyzer(t)

	for _, scenario := range entryImplementationCases {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
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

// TestCommandEntryForwardsPackageScopeValues checks value forwarding
// without exempting package initialization.
func TestCommandEntryForwardsPackageScopeValues(t *testing.T) {
	t.Parallel()
	binary := buildAnalyzer(t)
	fixture := analyzerFixture(t)
	writeEntryApplication(t, fixture)
	writeFixtureFile(t, fixture, "app/pattern.go", `package app

import "regexp"

func AcceptPattern(pattern *regexp.Regexp) {}
`)
	writeFixtureFile(t, fixture, "cmd/tool/main.go", `package main

import (
	"example.com/fixture/app"
	"log/slog"
	"regexp"
)

var goFindingPattern = regexp.MustCompile("finding")

func main() {
	slog.Debug("command invoked")
	app.AcceptPattern(goFindingPattern)
}
`)
	output := analyzeFixture(t, binary, fixture, true, "-commandentry", "-missing_boundary_log", "./cmd/tool")
	hasDeclaration := strings.Contains(output, "goFindingPattern contains implementation")
	rejectsEntry := strings.Contains(output, "main contains implementation")
	if !hasDeclaration || rejectsEntry || strings.Count(output, "commandentry:") != 1 {
		t.Fatalf("forwarding a global changed the declaration policy or rejected the entry point:\n%s", output)
	}
}

// TestDelegatingMainRequiresBoundaryLog checks that delegation preserves
// the process logging requirement.
func TestDelegatingMainRequiresBoundaryLog(t *testing.T) {
	t.Parallel()
	binary := buildAnalyzer(t)
	fixture := analyzerFixture(t)
	writeEntryApplication(t, fixture)
	writeFixtureFile(t, fixture, "cmd/tool/main.go", `package main

import "example.com/fixture/app"

func main() { app.Code(nil) }
`)
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

func Run(arguments []string) error           { return nil }
func Code(arguments []string) int            { return 0 }
func Generic[T any](arguments []string) int  { return 0 }
func Result(arguments []string) (int, error) { return 0, nil }

type Service struct{}

func (Service) Run()                                              {}
func Count() int                                                  { return 11 }
func Delete()                                                     {}
func WithCallback(run func([]string) int, arguments []string) int { return run(arguments) }
func Command(run func([]string) int, noFiles error) (string, []string, error) {
	return "", nil, noFiles
}
`)
}
