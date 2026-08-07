package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"goodkind.io/go-makefile/internal/report"
)

const govulncheckExit3HelperEnv = "GO_MK_TEST_GOVULNCHECK_EXIT_3"

func TestGovulncheckInstallationFailureIsAdvisory(t *testing.T) {
	result := runGovulncheckStepWith(govulncheckConfig{
		install: func(string) error {
			return errors.New("install unavailable")
		},
	})

	if result.Status != report.StatusAdvisory {
		t.Fatalf("status = %v, want advisory", result.Status)
	}
	if !strings.Contains(strings.Join(result.Findings, "\n"), "install unavailable") {
		t.Fatalf("findings = %v, want installation error", result.Findings)
	}
}

func TestPrepareGovulncheckCheckInstallsBeforeRun(t *testing.T) {
	calls := []string{}
	prepared := prepareGovulncheckCheck(govulncheckConfig{
		install: func(string) error {
			calls = append(calls, "install")
			return nil
		},
		goPath: func(string) (string, error) {
			calls = append(calls, "gopath")
			return "/tmp", nil
		},
		run: func(string, []string) ([]byte, error) {
			calls = append(calls, "run")
			return nil, nil
		},
	})
	if got := strings.Join(calls, ","); got != "install" {
		t.Fatalf("prepare calls = %q, want install", got)
	}
	result, status := prepared.run()
	if result.Status != report.StatusOK || status != 0 {
		t.Fatalf("result/status = %#v/%d, want ok/0", result, status)
	}
	if got := strings.Join(calls, ","); got != "install,gopath,run" {
		t.Fatalf("all calls = %q, want install,gopath,run", got)
	}
}

func TestGovulncheckUsesGOBIN(t *testing.T) {
	queriedName := ""
	runBinary := ""
	result := runGovulncheckStepWith(govulncheckConfig{
		install: func(string) error {
			return nil
		},
		goPath: func(name string) (string, error) {
			queriedName = name
			if name != "GOBIN" {
				return "", errors.New("unexpected Go path lookup")
			}
			return filepath.Join("custom", "bin"), nil
		},
		run: func(binary string, _ []string) ([]byte, error) {
			runBinary = binary
			return nil, nil
		},
	})

	if result.Status != report.StatusOK {
		t.Fatalf("status = %v, want ok", result.Status)
	}
	if queriedName != "GOBIN" {
		t.Fatalf("Go path lookup = %q, want GOBIN", queriedName)
	}
	wantBinary := filepath.Join("custom", "bin", "govulncheck")
	if runBinary != wantBinary {
		t.Fatalf("binary = %q, want %q", runBinary, wantBinary)
	}
}

func TestGovulncheckFallsBackToGOPATHBin(t *testing.T) {
	queries := []string{}
	runBinary := ""
	result := runGovulncheckStepWith(govulncheckConfig{
		install: func(string) error {
			return nil
		},
		goPath: func(name string) (string, error) {
			queries = append(queries, name)
			if name == "GOBIN" {
				return "", nil
			}
			if name == "GOPATH" {
				return filepath.Join("custom", "go"), nil
			}
			return "", errors.New("unexpected Go path lookup")
		},
		run: func(binary string, _ []string) ([]byte, error) {
			runBinary = binary
			return nil, nil
		},
	})

	if result.Status != report.StatusOK {
		t.Fatalf("status = %v, want ok", result.Status)
	}
	if got := strings.Join(queries, ","); got != "GOBIN,GOPATH" {
		t.Fatalf("Go path lookups = %q, want GOBIN,GOPATH", got)
	}
	wantBinary := filepath.Join("custom", "go", "bin", "govulncheck")
	if runBinary != wantBinary {
		t.Fatalf("binary = %q, want %q", runBinary, wantBinary)
	}
}

func TestGovulncheckUsesFirstGOPATHEntry(t *testing.T) {
	runBinary := ""
	result := runGovulncheckStepWith(govulncheckConfig{
		install: func(string) error {
			return nil
		},
		goPath: func(name string) (string, error) {
			if name == "GOBIN" {
				return "", nil
			}
			if name == "GOPATH" {
				paths := []string{filepath.Join("first", "go"), filepath.Join("second", "go")}
				return strings.Join(paths, string(os.PathListSeparator)), nil
			}
			return "", errors.New("unexpected Go path lookup")
		},
		run: func(binary string, _ []string) ([]byte, error) {
			runBinary = binary
			return nil, nil
		},
	})

	if result.Status != report.StatusOK {
		t.Fatalf("status = %v, want ok", result.Status)
	}
	wantBinary := filepath.Join("first", "go", "bin", "govulncheck")
	if runBinary != wantBinary {
		t.Fatalf("binary = %q, want %q", runBinary, wantBinary)
	}
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
	result := runGovulncheckStepWith(govulncheckConfig{
		install: func(string) error {
			return nil
		},
		goPath: func(string) (string, error) {
			return "/tmp/go", nil
		},
		run: func(string, []string) ([]byte, error) {
			command := exec.Command(os.Args[0], "-test.run=^TestGovulncheckExit3Helper$")
			command.Env = append(os.Environ(), govulncheckExit3HelperEnv+"=1")
			return []byte("GO-2026-1234 affects example/package\n"), command.Run()
		},
	})

	if result.Status != report.StatusAdvisory {
		t.Fatalf("status = %v, want advisory", result.Status)
	}
	if !strings.Contains(strings.Join(result.Findings, "\n"), "GO-2026-1234") {
		t.Fatalf("findings = %v, want vulnerability output", result.Findings)
	}
	if strings.Contains(strings.Join(result.Findings, "\n"), "execution failed") {
		t.Fatalf("findings = %v, want findings without execution failure", result.Findings)
	}
}

func TestGovulncheckExit3Helper(t *testing.T) {
	if os.Getenv(govulncheckExit3HelperEnv) != "1" {
		return
	}
	os.Exit(3)
}

func TestGovulncheckExecutionFailureIsAdvisory(t *testing.T) {
	result := runGovulncheckStepWith(govulncheckConfig{
		install: func(string) error {
			return nil
		},
		goPath: func(string) (string, error) {
			return "/tmp/go", nil
		},
		run: func(string, []string) ([]byte, error) {
			return nil, errors.New("execution unavailable")
		},
	})

	if result.Status != report.StatusAdvisory {
		t.Fatalf("status = %v, want advisory", result.Status)
	}
	if !strings.Contains(strings.Join(result.Findings, "\n"), "execution unavailable") {
		t.Fatalf("findings = %v, want execution error", result.Findings)
	}
}
