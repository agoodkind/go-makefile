package selfupdate

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRenderLaunchdPlistIncludesProgramArguments(t *testing.T) {
	rendered, err := RenderLaunchdPlist(LaunchdServiceOptions{
		Label:       "io.goodkind.go-mk.selfupdate",
		ProgramPath: "/usr/local/bin/go-mk",
		Arguments:   []string{"selfupdate", "watch"},
		PlistPath:   "/tmp/io.goodkind.go-mk.selfupdate.plist",
		LogPath:     "/tmp/go-mk-selfupdate.log",
		RunAtLoad:   true,
		KeepAlive:   true,
	})

	if err != nil {
		t.Fatalf("RenderLaunchdPlist() error: %v", err)
	}
	for _, want := range []string{
		"<string>/usr/local/bin/go-mk</string>",
		"<string>selfupdate</string>",
		"<string>watch</string>",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("launchd plist missing %q\n%s", want, rendered)
		}
	}
}

func TestInstallLaunchdServiceSkipsRestartWhenPlistUnchangedAndLoaded(t *testing.T) {
	restoreServiceSeams(t)
	tempDir := t.TempDir()
	plistPath := filepath.Join(tempDir, "io.goodkind.demo.plist")
	logPath := filepath.Join(tempDir, "demo.log")
	options := LaunchdServiceOptions{
		Label:       "io.goodkind.demo",
		ProgramPath: "/usr/local/bin/demo",
		PlistPath:   plistPath,
		LogPath:     logPath,
		RunAtLoad:   true,
		KeepAlive:   true,
		Environment: []EnvironmentPair{{Name: "GK_MODE", Value: "test"}},
	}
	rendered, err := RenderLaunchdPlist(options)
	if err != nil {
		t.Fatalf("RenderLaunchdPlist() error: %v", err)
	}
	if err := os.WriteFile(plistPath, []byte(rendered), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}
	calls := []string{}
	serviceRunProcess = func(name string, args []string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "launchctl" && slices.Equal(args, []string{"print", "gui/501/io.goodkind.demo"}) {
			return nil
		}
		return errors.New("unexpected process")
	}
	serviceCurrentUserID = func() string {
		return "501"
	}
	var stdout bytes.Buffer
	options.Stdout = &stdout

	err = InstallLaunchdService(options)
	if err != nil {
		t.Fatalf("InstallLaunchdService() error: %v", err)
	}
	wantCalls := []string{"launchctl print gui/501/io.goodkind.demo"}
	if !slices.Equal(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	if !strings.Contains(stdout.String(), "unchanged and loaded; skipping bootout/bootstrap") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestInstallSystemdUserServiceRestartsWhenUnitChanged(t *testing.T) {
	restoreServiceSeams(t)
	tempDir := t.TempDir()
	unitPath := filepath.Join(tempDir, "demo.service")
	options := SystemdUserServiceOptions{
		Unit:          "demo.service",
		ProgramPath:   "/usr/local/bin/demo",
		Arguments:     []string{"serve"},
		UnitPath:      unitPath,
		Description:   "Demo Service",
		Documentation: "https://example.invalid/demo",
		Restart:       "always",
		RestartSec:    "5",
		Environment:   []EnvironmentPair{{Name: "GK_MODE", Value: "test"}},
	}
	calls := []string{}
	serviceRunProcess = func(name string, args []string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	var stdout bytes.Buffer
	options.Stdout = &stdout

	err := InstallSystemdUserService(options)
	if err != nil {
		t.Fatalf("InstallSystemdUserService() error: %v", err)
	}
	wantCalls := []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable demo.service",
		"systemctl --user restart demo.service",
	}
	if !slices.Equal(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	unitBytes, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("ReadFile() unit error: %v", err)
	}
	unit := string(unitBytes)
	for _, want := range []string{
		"Description=Demo Service",
		"Documentation=https://example.invalid/demo",
		"ExecStart=/usr/local/bin/demo serve",
		"Restart=always",
		"RestartSec=5",
		`Environment="GK_MODE=test"`,
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q\n%s", want, unit)
		}
	}
	if !strings.Contains(stdout.String(), "installed: "+unitPath) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestParseEnvironmentPairs(t *testing.T) {
	pairs, err := ParseEnvironmentPairs([]string{"GK_MODE=test", "EMPTY="})
	if err != nil {
		t.Fatalf("ParseEnvironmentPairs() error: %v", err)
	}
	wantPairs := []EnvironmentPair{
		{Name: "GK_MODE", Value: "test"},
		{Name: "EMPTY", Value: ""},
	}
	if !slices.Equal(pairs, wantPairs) {
		t.Fatalf("pairs = %#v, want %#v", pairs, wantPairs)
	}
}

func restoreServiceSeams(t *testing.T) {
	t.Helper()
	originalRunProcess := serviceRunProcess
	originalCurrentUserID := serviceCurrentUserID
	t.Cleanup(func() {
		serviceRunProcess = originalRunProcess
		serviceCurrentUserID = originalCurrentUserID
	})
}
