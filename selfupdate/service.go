package selfupdate

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// EnvironmentPair is one KEY=VALUE entry for a launchd plist or systemd unit.
type EnvironmentPair struct {
	Name  string
	Value string
}

// LaunchdServiceOptions configures a macOS launchd user service install.
type LaunchdServiceOptions struct {
	Label       string
	ProgramPath string
	Arguments   []string
	PlistPath   string
	LogPath     string
	Environment []EnvironmentPair
	RunAtLoad   bool
	KeepAlive   bool
	Domain      string
	Stdout      io.Writer
}

// SystemdUserServiceOptions configures a Linux systemd user service install.
type SystemdUserServiceOptions struct {
	Unit          string
	ProgramPath   string
	Arguments     []string
	UnitPath      string
	Description   string
	Documentation string
	Restart       string
	RestartSec    string
	Environment   []EnvironmentPair
	Stdout        io.Writer
}

var (
	serviceRunProcess    = runServiceProcess
	serviceCurrentUserID = func() string {
		return strconv.Itoa(os.Getuid())
	}
)

// ParseEnvironmentPairs parses KEY=VALUE strings into service environment pairs.
func ParseEnvironmentPairs(entries []string) ([]EnvironmentPair, error) {
	pairs := make([]EnvironmentPair, 0, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("environment entries must be KEY=VALUE")
		}
		pairs = append(pairs, EnvironmentPair{Name: key, Value: value})
	}
	return pairs, nil
}

// InstallLaunchdService renders, installs, and loads a launchd user service.
func InstallLaunchdService(options LaunchdServiceOptions) error {
	slog.Info("install launchd service", slog.String("label", options.Label), slog.String("plist_path", options.PlistPath))
	if err := validateLaunchdServiceOptions(options); err != nil {
		return err
	}
	rendered, err := RenderLaunchdPlist(options)
	if err != nil {
		return err
	}
	stdout := serviceStdout(options.Stdout)
	if err := os.MkdirAll(filepath.Dir(options.PlistPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(options.LogPath), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(options.LogPath, os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if err := logFile.Close(); err != nil {
		return err
	}
	tempPath, cleanup, err := writeServiceTempFile(options.PlistPath, rendered)
	if err != nil {
		return err
	}
	defer cleanup()
	domain := options.Domain
	if domain == "" {
		domain = "gui/" + serviceCurrentUserID()
	}
	if serviceFilesEqual(tempPath, options.PlistPath) && launchdServiceLoaded(domain, options.Label) {
		fmt.Fprintf(stdout, "service-install: %s unchanged and loaded; skipping bootout/bootstrap\n", options.PlistPath)
		fmt.Fprintf(stdout, "  logs: %s\n", options.LogPath)
		return nil
	}
	if err := os.Rename(tempPath, options.PlistPath); err != nil {
		return err
	}
	_ = serviceRunProcess("launchctl", []string{"bootout", domain, options.PlistPath})
	if err := serviceRunProcess("launchctl", []string{"bootstrap", domain, options.PlistPath}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "installed: %s\n", options.PlistPath)
	fmt.Fprintf(stdout, "  logs: %s\n", options.LogPath)
	return nil
}

// InstallSystemdUserService renders, installs, enables, and restarts a user unit.
func InstallSystemdUserService(options SystemdUserServiceOptions) error {
	slog.Info("install systemd user service", slog.String("unit", options.Unit), slog.String("unit_path", options.UnitPath))
	if err := validateSystemdUserServiceOptions(options); err != nil {
		return err
	}
	rendered, err := RenderSystemdUserUnit(options)
	if err != nil {
		return err
	}
	stdout := serviceStdout(options.Stdout)
	if err := os.MkdirAll(filepath.Dir(options.UnitPath), 0o755); err != nil {
		return err
	}
	tempPath, cleanup, err := writeServiceTempFile(options.UnitPath, rendered)
	if err != nil {
		return err
	}
	defer cleanup()
	if serviceFilesEqual(tempPath, options.UnitPath) && systemdUserServiceActive(options.Unit) {
		fmt.Fprintf(stdout, "service-install: %s unchanged and active; skipping daemon-reload/restart\n", options.UnitPath)
		fmt.Fprintf(stdout, "  logs: journalctl --user -u %s -f\n", options.Unit)
		return nil
	}
	if err := os.Rename(tempPath, options.UnitPath); err != nil {
		return err
	}
	if err := serviceRunProcess("systemctl", []string{"--user", "daemon-reload"}); err != nil {
		return err
	}
	if err := serviceRunProcess("systemctl", []string{"--user", "enable", options.Unit}); err != nil {
		return err
	}
	if err := serviceRunProcess("systemctl", []string{"--user", "restart", options.Unit}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "installed: %s\n", options.UnitPath)
	fmt.Fprintf(stdout, "  logs: journalctl --user -u %s -f\n", options.Unit)
	return nil
}

// RenderLaunchdPlist renders the launchd plist installed by InstallLaunchdService.
func RenderLaunchdPlist(options LaunchdServiceOptions) (string, error) {
	if err := validateEnvironmentPairs(options.Environment); err != nil {
		return "", err
	}
	var builder strings.Builder
	builder.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	builder.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" ")
	builder.WriteString("\"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	builder.WriteString("<plist version=\"1.0\">\n<dict>\n")
	writePlistKeyString(&builder, "Label", options.Label)
	builder.WriteString("    <key>ProgramArguments</key>\n    <array>\n")
	builder.WriteString("        <string>" + xmlEscape(options.ProgramPath) + "</string>\n")
	for _, argument := range options.Arguments {
		builder.WriteString("        <string>" + xmlEscape(argument) + "</string>\n")
	}
	builder.WriteString("    </array>\n")
	writePlistKeyString(&builder, "StandardOutPath", options.LogPath)
	writePlistKeyString(&builder, "StandardErrorPath", options.LogPath)
	writePlistKeyBool(&builder, "RunAtLoad", options.RunAtLoad)
	writePlistKeyBool(&builder, "KeepAlive", options.KeepAlive)
	if len(options.Environment) > 0 {
		builder.WriteString("    <key>EnvironmentVariables</key>\n    <dict>\n")
		for _, pair := range options.Environment {
			writePlistKeyString(&builder, pair.Name, pair.Value)
		}
		builder.WriteString("    </dict>\n")
	}
	builder.WriteString("</dict>\n</plist>\n")
	return builder.String(), nil
}

// RenderSystemdUserUnit renders the unit installed by InstallSystemdUserService.
func RenderSystemdUserUnit(options SystemdUserServiceOptions) (string, error) {
	if err := validateEnvironmentPairs(options.Environment); err != nil {
		return "", err
	}
	description := options.Description
	if description == "" {
		description = strings.TrimSuffix(options.Unit, ".service")
	}
	var builder strings.Builder
	builder.WriteString("[Unit]\n")
	builder.WriteString("Description=" + description + "\n")
	if options.Documentation != "" {
		builder.WriteString("Documentation=" + options.Documentation + "\n")
	}
	builder.WriteString("\n[Service]\n")
	builder.WriteString("ExecStart=" + systemdCommandLine(options.ProgramPath, options.Arguments) + "\n")
	if options.Restart != "" {
		builder.WriteString("Restart=" + options.Restart + "\n")
	}
	if options.RestartSec != "" {
		builder.WriteString("RestartSec=" + options.RestartSec + "\n")
	}
	for _, pair := range options.Environment {
		builder.WriteString("Environment=\"" + systemdEscape(pair.Name+"="+pair.Value) + "\"\n")
	}
	builder.WriteString("\n[Install]\n")
	builder.WriteString("WantedBy=default.target\n")
	return builder.String(), nil
}

func validateLaunchdServiceOptions(options LaunchdServiceOptions) error {
	if strings.TrimSpace(options.Label) == "" {
		return fmt.Errorf("launchd-service: label is required")
	}
	if strings.TrimSpace(options.ProgramPath) == "" {
		return fmt.Errorf("launchd-service: program path is required")
	}
	if strings.TrimSpace(options.PlistPath) == "" {
		return fmt.Errorf("launchd-service: plist path is required")
	}
	if strings.TrimSpace(options.LogPath) == "" {
		return fmt.Errorf("launchd-service: log path is required")
	}
	return validateEnvironmentPairs(options.Environment)
}

func validateSystemdUserServiceOptions(options SystemdUserServiceOptions) error {
	if strings.TrimSpace(options.Unit) == "" {
		return fmt.Errorf("systemd-user-service: unit is required")
	}
	if strings.TrimSpace(options.ProgramPath) == "" {
		return fmt.Errorf("systemd-user-service: program path is required")
	}
	if strings.TrimSpace(options.UnitPath) == "" {
		return fmt.Errorf("systemd-user-service: unit path is required")
	}
	return validateEnvironmentPairs(options.Environment)
}

func validateEnvironmentPairs(pairs []EnvironmentPair) error {
	for _, pair := range pairs {
		if strings.TrimSpace(pair.Name) == "" || strings.Contains(pair.Name, "=") {
			return fmt.Errorf("environment entries must be KEY=VALUE")
		}
	}
	return nil
}

func launchdServiceLoaded(domain string, label string) bool {
	err := serviceRunProcess("launchctl", []string{"print", domain + "/" + label})
	return err == nil
}

func systemdUserServiceActive(unit string) bool {
	err := serviceRunProcess("systemctl", []string{"--user", "is-active", unit})
	return err == nil
}

func writeServiceTempFile(path string, content string) (string, func(), error) {
	slog.Info("install service write temp file", slog.String("path", path))
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return "", func() {}, err
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = os.Remove(temporaryPath)
	}
	if _, err := temporary.WriteString(content); err != nil {
		_ = temporary.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := os.Chmod(temporaryPath, 0o644); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return temporaryPath, cleanup, nil
}

func serviceFilesEqual(leftPath string, rightPath string) bool {
	left, leftErr := os.ReadFile(leftPath)
	if leftErr != nil {
		return false
	}
	right, rightErr := os.ReadFile(rightPath)
	if rightErr != nil {
		return false
	}
	return bytes.Equal(left, right)
}

func serviceStdout(writer io.Writer) io.Writer {
	if writer == nil {
		return io.Discard
	}
	return writer
}

func writePlistKeyString(builder *strings.Builder, key string, value string) {
	builder.WriteString("    <key>" + xmlEscape(key) + "</key>\n")
	builder.WriteString("    <string>" + xmlEscape(value) + "</string>\n")
}

func writePlistKeyBool(builder *strings.Builder, key string, value bool) {
	builder.WriteString("    <key>" + xmlEscape(key) + "</key>\n")
	if value {
		builder.WriteString("    <true/>\n")
		return
	}
	builder.WriteString("    <false/>\n")
}

func xmlEscape(value string) string {
	var buffer bytes.Buffer
	_ = xml.EscapeText(&buffer, []byte(value))
	return buffer.String()
}

func systemdEscape(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

func systemdCommandLine(programPath string, arguments []string) string {
	parts := make([]string, 0, len(arguments)+1)
	parts = append(parts, systemdQuoteCommandPart(programPath))
	for _, argument := range arguments {
		parts = append(parts, systemdQuoteCommandPart(argument))
	}
	return strings.Join(parts, " ")
}

func systemdQuoteCommandPart(value string) string {
	if value == "" {
		return `""`
	}
	if !strings.ContainsAny(value, " \t\n\"\\") {
		return value
	}
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

func runServiceProcess(name string, args []string) error {
	slog.Info("install service run process", slog.String("name", name), slog.Any("args", args))
	command := exec.Command(name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		slog.Warn("install service process failed", slog.String("name", name), slog.Any("args", args), slog.String("output", strings.TrimSpace(string(output))), slog.Any("err", err))
		return fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}
