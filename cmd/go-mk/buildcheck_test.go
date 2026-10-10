package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"goodkind.io/go-makefile/internal/report"
)

func TestGovulncheckInstallationFailureIsAdvisory(t *testing.T) {
	dir, env := govulncheckProject(t)
	env["GOVULNCHECK_INSTALL"] = "example.invalid/unavailable/cmd/govulncheck@v0.0.0"
	output := govulncheckCommand(t, dir, env, "govulncheck")
	govulncheckRequireOutput(t, output, "ADVISORY", "module lookup disabled by GOPROXY=off",
		"installation failed: exit status 1")
	if strings.Contains(output, "govulncheck execution failed") {
		t.Fatalf("installation failure continued to execution:\n%s", output)
	}
}

func TestPrepareGovulncheckCheckInstallsBeforeRun(t *testing.T) {
	dir, env := govulncheckProject(t)
	govulncheckCopyBinary(t, env["GOBIN"])
	writeFile(t, filepath.Join(dir, "install", "main.go"), "package main\n\nfunc main() {}\n")
	// These installs compile the fixture's own command because no lint gate runs.
	for _, name := range []string{
		"GOLANGCI_LINT_INSTALL", "GOFUMPT_INSTALL", "GOIMPORTS_INSTALL",
		"GOCYCLO_INSTALL", "DEADCODE_INSTALL",
	} {
		env[name] = "./install"
	}
	env["LINT_GATES"] = " "
	env["STATICCHECK_EXTRA_BUILD_REPO"] = filepath.Join(repoRootForTest(t), "staticcheck")
	env["STATICCHECK_EXTRA_BUILD_PKG"] = "./cmd/staticcheck-extra"
	writeFile(t, filepath.Join(dir, ".make", "go-latest-version"), "1.27.1\n")
	output := govulncheckCommand(t, dir, env, "build-check")
	govulncheckRequireFindings(t, output)
	if _, err := os.Stat(filepath.Join(env["GOBIN"], "probe")); err != nil {
		t.Fatalf("Go did not install the fixture command: %v", err)
	}
	records := readGoMkLogRecords(t, dir)
	installIndex := strings.Index(records, `"spec":"."`)
	vetIndex := strings.Index(records, `"tool":"vet"`)
	runIndex := strings.Index(records, `"tool":"govulncheck"`)
	if installIndex < 0 || vetIndex < installIndex || runIndex < vetIndex {
		t.Fatalf("govulncheck installation must precede vet and scanning:\n%s", records)
	}
}

func TestGovulncheckUsesGOBIN(t *testing.T) {
	dir, env := govulncheckProject(t)
	govulncheckCopyBinary(t, env["GOBIN"])
	output := govulncheckCommand(t, dir, env, "govulncheck")
	govulncheckRequireFindings(t, output)
}

func TestGovulncheckFallsBackToGOPATHBin(t *testing.T) {
	dir, env := govulncheckProject(t)
	env["GOBIN"] = ""
	env["PATH"] = govulncheckFallbackBin + string(os.PathListSeparator) + os.Getenv("PATH")
	govulncheckCopyBinary(t, filepath.Join(env["GOPATH"], "bin"))
	output := govulncheckCommand(t, dir, env, "govulncheck")
	govulncheckRequireFindings(t, output)
}

func TestGovulncheckUsesFirstGOPATHEntry(t *testing.T) {
	dir, env := govulncheckProject(t)
	env["GOBIN"] = ""
	env["PATH"] = govulncheckFallbackBin + string(os.PathListSeparator) + os.Getenv("PATH")
	first := env["GOPATH"]
	env["GOPATH"] = first + string(os.PathListSeparator) + filepath.Join(dir, "second")
	govulncheckCopyBinary(t, filepath.Join(first, "bin"))
	output := govulncheckCommand(t, dir, env, "govulncheck")
	govulncheckRequireFindings(t, output)
}

func TestGovulncheckEmptyGOPATHIsAdvisory(t *testing.T) {
	result := runGovulncheckStepWith(govulncheckConfig{
		install: func(string) error {
			return nil
		},
		goPath: func(string) (string, error) {
			return "", nil
		},
		run: func(string, []string) ([]byte, error) {
			return nil, nil
		},
	})

	if result.Status != report.StatusAdvisory {
		t.Fatalf("status = %v, want advisory", result.Status)
	}
	if !strings.Contains(strings.Join(result.Findings, "\n"), "GOPATH") {
		t.Fatalf("findings = %v, want GOPATH lookup error", result.Findings)
	}
}

func TestGovulncheckGOBINLookupFailureIsAdvisory(t *testing.T) {
	result := runGovulncheckStepWith(govulncheckConfig{
		install: func(string) error {
			return nil
		},
		goPath: func(name string) (string, error) {
			if name == "GOBIN" {
				return "", errors.New("GOBIN unavailable")
			}
			return filepath.Join("custom", "go"), nil
		},
		run: func(string, []string) ([]byte, error) {
			return nil, nil
		},
	})

	if result.Status != report.StatusAdvisory {
		t.Fatalf("status = %v, want advisory", result.Status)
	}
	if !strings.Contains(strings.Join(result.Findings, "\n"), "GOBIN unavailable") {
		t.Fatalf("findings = %v, want GOBIN lookup error", result.Findings)
	}
}

func TestGovulncheckFindingsAreAdvisory(t *testing.T) {
	dir, env := govulncheckProject(t)
	govulncheckCopyBinary(t, env["GOBIN"])
	scanner := exec.Command(filepath.Join(env["GOBIN"], "govulncheck"), "-db", env["GOVULNDB"], "./...")
	scanner.Dir = dir
	scanner.Env = testProcessEnvironment(env)
	output, err := scanner.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 3 {
		t.Fatalf("govulncheck exit = %v, want status 3:\n%s", err, output)
	}
	govulncheckRequireFindings(t, govulncheckCommand(t, dir, env, "govulncheck"))
}

func TestGovulncheckExecutionFailureIsAdvisory(t *testing.T) {
	dir, env := govulncheckProject(t)
	govulncheckCopyBinary(t, env["GOBIN"])
	missing := filepath.Join(dir, "missing-database")
	env["GOVULNDB"] = (&url.URL{Scheme: "file", Path: missing}).String()
	env["GOVULNCHECK_TARGETS"] = "-db " + env["GOVULNDB"] + " ./..."
	output := govulncheckCommand(t, dir, env, "govulncheck")
	govulncheckRequireOutput(t, output, "ADVISORY", missing,
		"govulncheck execution failed: exit status 1")
}

// Go 1.27 computes GOBIN from GOPATH before the engine can use its fallback.
const govulncheckFallbackToolchain = "go1.26.4"

var (
	govulncheckToolsOnce   sync.Once
	govulncheckToolsDir    string
	govulncheckToolsErr    error
	govulncheckCaches      []string
	govulncheckFallbackBin string
)

func govulncheckTools(t *testing.T) string {
	t.Helper()
	govulncheckToolsOnce.Do(func() {
		govulncheckToolsDir, govulncheckToolsErr = os.MkdirTemp("", "go-mk-govulncheck.")
		if govulncheckToolsErr != nil {
			return
		}
		telemetryDir := filepath.Join(govulncheckToolsDir, "telemetry")
		writeFile(t, filepath.Join(telemetryDir, "mode"), "off\n")
		// Go computes the ambient cache paths without reading the user's Go settings.
		cacheCommand := exec.Command("go", "env", "GOMODCACHE", "GOCACHE")
		cacheCommand.Env = testProcessEnvironment(map[string]string{
			"GOENV": "off", "GOFLAGS": "-modcacherw", "TEST_TELEMETRY_DIR": telemetryDir,
			"GOMODCACHE": os.Getenv("GOMODCACHE"), "GOCACHE": os.Getenv("GOCACHE"),
			"GOPATH": os.Getenv("GOPATH"),
		})
		cacheOutput, err := cacheCommand.CombinedOutput()
		if err != nil {
			govulncheckToolsErr = fmt.Errorf("resolve Go caches: %w\n%s", err, cacheOutput)
			return
		}
		govulncheckCaches = strings.Fields(string(cacheOutput))
		if len(govulncheckCaches) != 2 {
			govulncheckToolsErr = fmt.Errorf("Go returned unexpected cache paths: %s", cacheOutput)
			return
		}
		root, err := filepath.Abs("../..")
		if err != nil {
			govulncheckToolsErr = err
			return
		}
		goMk, err := os.ReadFile(filepath.Join(root, "go.mk"))
		if err != nil {
			govulncheckToolsErr = err
			return
		}
		var installSpec string
		for _, line := range strings.Split(string(goMk), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "GOVULNCHECK_INSTALL" && fields[1] == "?=" {
				installSpec = fields[2]
			}
		}
		if installSpec == "" {
			govulncheckToolsErr = errors.New("go.mk has no GOVULNCHECK_INSTALL pin")
			return
		}
		command := exec.Command("go", "install", installSpec)
		command.Dir = govulncheckToolsDir
		command.Env = testProcessEnvironment(map[string]string{
			"HOME": govulncheckToolsDir, "GOENV": "off", "GOFLAGS": "-modcacherw",
			"TEST_TELEMETRY_DIR": telemetryDir,
			"GOWORK":             "off", "GOTOOLCHAIN": "local",
			"GOBIN": govulncheckToolsDir, "GOPATH": filepath.Join(govulncheckToolsDir, "go"),
			"GOMODCACHE": govulncheckCaches[0], "GOCACHE": govulncheckCaches[1],
			"GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org",
		})
		if output, err := command.CombinedOutput(); err != nil {
			govulncheckToolsErr = fmt.Errorf("install pinned govulncheck %s: %w\n%s", installSpec, err, output)
			return
		}
		command = exec.Command("go", "env", "GOROOT")
		command.Dir = govulncheckToolsDir
		command.Env = testProcessEnvironment(map[string]string{
			"HOME": govulncheckToolsDir, "GOENV": "off", "GOFLAGS": "-modcacherw",
			"TEST_TELEMETRY_DIR": telemetryDir,
			"GOWORK":             "off", "GOTOOLCHAIN": govulncheckFallbackToolchain,
			"GOBIN": "", "GOPATH": filepath.Join(govulncheckToolsDir, "go"),
			"GOMODCACHE": govulncheckCaches[0], "GOCACHE": govulncheckCaches[1],
			"GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org",
		})
		var stderr strings.Builder
		command.Stderr = &stderr
		output, err := command.Output()
		if err != nil {
			govulncheckToolsErr = fmt.Errorf("download fallback Go toolchain %s: %w\n%s",
				govulncheckFallbackToolchain, err, stderr.String())
			return
		}
		govulncheckFallbackBin = filepath.Join(strings.TrimSpace(string(output)), "bin")
	})
	if govulncheckToolsErr != nil {
		t.Fatalf("prepare real govulncheck: %v", govulncheckToolsErr)
	}
	return filepath.Join(govulncheckToolsDir, "govulncheck")
}

func govulncheckProject(t *testing.T) (string, map[string]string) {
	t.Helper()
	govulncheckTools(t)
	dir := t.TempDir()
	env := map[string]string{
		"HOME": dir, "GOENV": "off", "XDG_CONFIG_HOME": filepath.Join(dir, ".config"),
		"GOWORK": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "-modcacherw",
		"GOOS": "", "GOARCH": "", "CGO_ENABLED": "0",
		"GOBIN": filepath.Join(dir, "custom-bin"), "GOPATH": filepath.Join(dir, "go"),
		"GOMODCACHE": govulncheckCaches[0], "GOCACHE": govulncheckCaches[1],
		"GOPROXY": "off", "GOSUMDB": "off",
		"GIT_CONFIG_GLOBAL": filepath.Join(dir, "gitconfig"), "GIT_CONFIG_SYSTEM": "/dev/null",
		"GIT_AUTHOR_NAME": "Fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid",
		"GIT_COMMITTER_NAME": "Fixture", "GIT_COMMITTER_EMAIL": "fixture@example.invalid",
		// Installing the fixture project does not overwrite the copied scanner.
		"GOVULNCHECK_INSTALL": ".", "GOVULNCHECK_TARGETS": "./...",
	}
	for name, value := range env {
		t.Setenv(name, value)
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("resolve fixture Go telemetry directory: %v", err)
	}
	// GOTELEMETRY is not settable. The mode file prevents telemetry child processes.
	writeFile(t, filepath.Join(configDir, "go", "telemetry", "mode"), "off\n")
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/probe\n\ngo 1.24\n")
	writeFile(t, filepath.Join(dir, "main.go"),
		"package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"probe\") }\n")
	database := filepath.Join(dir, "database")
	writeFile(t, filepath.Join(database, "index", "db.json"),
		`{"modified":"2026-01-01T00:00:00Z"}`)
	writeFile(t, filepath.Join(database, "index", "modules.json"),
		`[{"path":"stdlib","vulns":[{"id":"GO-2026-1234","modified":"2026-01-01T00:00:00Z"}]}]`)
	writeFile(t, filepath.Join(database, "ID", "GO-2026-1234.json"), `{
		"schema_version":"1.3.1",
		"id":"GO-2026-1234",
		"modified":"2026-01-01T00:00:00Z",
		"published":"2026-01-01T00:00:00Z",
		"summary":"Fixture vulnerability in fmt.Println",
		"details":"The fixture project calls the affected symbol.",
		"database_specific":{"url":"https://example.invalid/GO-2026-1234"},
		"affected":[{
			"package":{"name":"stdlib","ecosystem":"Go"},
			"ranges":[{"type":"SEMVER","events":[{"introduced":"0"}]}],
			"ecosystem_specific":{"imports":[{"path":"fmt","symbols":["Println"]}]}
		}]
	}`)
	env["GOVULNDB"] = (&url.URL{Scheme: "file", Path: database}).String()
	// The pinned scanner accepts the database through -db and ignores GOVULNDB.
	env["GOVULNCHECK_TARGETS"] = "-db " + env["GOVULNDB"] + " ./..."
	t.Setenv("GOVULNDB", env["GOVULNDB"])
	t.Setenv("GOVULNCHECK_TARGETS", env["GOVULNCHECK_TARGETS"])
	return dir, env
}

func govulncheckCopyBinary(t *testing.T, directory string) {
	t.Helper()
	if err := copyFile(govulncheckTools(t), filepath.Join(directory, "govulncheck")); err != nil {
		t.Fatalf("copy real govulncheck: %v", err)
	}
}

func govulncheckCommand(t *testing.T, dir string, env map[string]string, subcommand string) string {
	t.Helper()
	command := exec.Command(builtTestEngine(t), subcommand)
	command.Dir = dir
	command.Env = testProcessEnvironment(env)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go-mk %s exit = %v, want success:\n%s", subcommand, err, output)
	}
	return string(output)
}

func govulncheckRequireFindings(t *testing.T, output string) {
	t.Helper()
	govulncheckRequireOutput(t, output, "ADVISORY", "GO-2026-1234", "fmt.Println")
	if strings.Contains(output, "execution failed") {
		t.Fatalf("vulnerability findings include an execution failure:\n%s", output)
	}
}

func govulncheckRequireOutput(t *testing.T, output string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(output, value) {
			t.Fatalf("output lacks %q:\n%s", value, output)
		}
	}
}
