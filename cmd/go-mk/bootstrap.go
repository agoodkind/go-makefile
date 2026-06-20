package main

import (
	"bufio"
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

//go:embed bootstrap_assets/bootstrap.mk bootstrap_assets/Makefile.tmpl
var bootstrapAssetFS embed.FS

const (
	defaultBootstrapVanityRoot = "goodkind.io"
	makefileAssetPath          = "bootstrap_assets/Makefile.tmpl"
	bootstrapMkAssetPath       = "bootstrap_assets/bootstrap.mk"
	generatedMakefileFirstLine = "# `make help` is the canonical source of truth for every target this repo"
)

type bootstrapOptions struct {
	modulePath   string
	vanityRoot   string
	forceLibrary bool
	forceBinary  bool
	yes          bool
	stdin        *os.File
	stdout       io.Writer
	stderr       io.Writer
}

type bootstrapContext struct {
	Binary  string
	Cmd     string
	Layout  string
	Vpkg    string
	BaseURL string
}

type generatedMakefileKey string

const (
	generatedKeyBinary      generatedMakefileKey = "BINARY"
	generatedKeyCmd         generatedMakefileKey = "CMD"
	generatedKeyVpkg        generatedMakefileKey = "VPKG"
	generatedKeyLibrary     generatedMakefileKey = "LIBRARY"
	generatedKeyGoMkModules generatedMakefileKey = "GO_MK_MODULES"
)

func registerBootstrapCommand(root *cobra.Command) {
	options := bootstrapOptions{}
	command := &cobra.Command{
		Use:   "bootstrap",
		Short: "Scaffold or repair a Go repo against go-makefile",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			options.stdin = os.Stdin
			options.stdout = os.Stdout
			options.stderr = os.Stderr
			recordedExit = statusFromError(runBootstrap(options))
			return nil
		},
	}
	command.Flags().StringVar(&options.modulePath, "module", "", "module path for go mod init")
	command.Flags().StringVar(&options.vanityRoot, "vanity", "", "vanity import root for inferred modules")
	command.Flags().BoolVar(&options.forceLibrary, "library", false, "force library layout")
	command.Flags().BoolVar(&options.forceBinary, "binary", false, "force binary layout")
	command.Flags().BoolVarP(&options.yes, "yes", "y", false, "accept inferred values without prompting")
	root.AddCommand(command)
}

func runBootstrap(options bootstrapOptions) error {
	slog.Info("bootstrap repo")
	if options.stdout == nil {
		options.stdout = io.Discard
	}
	if options.stderr == nil {
		options.stderr = io.Discard
	}
	if options.stdin == nil {
		options.stdin = os.Stdin
	}
	if options.forceLibrary && options.forceBinary {
		return errors.New("--library and --binary are mutually exclusive")
	}
	modulePath, err := ensureGoModule(options)
	if err != nil {
		return err
	}
	layout, err := resolveBootstrapLayout(options)
	if err != nil {
		return err
	}
	context, err := buildBootstrapContext(modulePath, layout)
	if err != nil {
		return err
	}
	printBootstrapSummary(options.stdout, modulePath, context)
	if err := reconcileMakefile(context, options.stdout, options.stderr); err != nil {
		return err
	}
	if err := reconcileBootstrapMk(options.stdout); err != nil {
		return err
	}
	warnIfLocalGolangCI(options.stderr)
	if err := reconcileGitignore(options.stdout); err != nil {
		return err
	}
	printBootstrapDone(options.stdout)
	return nil
}

func ensureGoModule(options bootstrapOptions) (string, error) {
	if fileExists("go.mod") {
		return readModulePath("go.mod")
	}
	modulePath, err := resolveNewModulePath(options)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(modulePath) == "" {
		return "", errors.New("module path is empty")
	}
	if _, err := exec.LookPath("go"); err != nil {
		return "", errors.New("go command not found on PATH")
	}
	fmt.Fprintf(options.stdout, "running: go mod init %s\n", modulePath)
	if err := runBootstrapProcess("go", []string{"mod", "init", modulePath}, options.stdout, options.stderr); err != nil {
		return "", err
	}
	return readModulePath("go.mod")
}

func resolveNewModulePath(options bootstrapOptions) (string, error) {
	if strings.TrimSpace(options.modulePath) != "" {
		return strings.TrimSpace(options.modulePath), nil
	}
	if modulePath := strings.TrimSpace(os.Getenv("GO_MODULE_PATH")); modulePath != "" {
		return modulePath, nil
	}
	inferred, err := inferModulePath(resolveVanityRoot(options))
	if err != nil {
		return "", err
	}
	if inferred == "" {
		return "", errors.New("no go.mod found and could not infer a module path. Pass --module=<path> or set GO_MODULE_PATH")
	}
	if term.IsTerminal(int(options.stdin.Fd())) && !options.yes {
		return promptWithDefault("module path", inferred, options.stdin, options.stderr)
	}
	if !options.yes {
		return "", fmt.Errorf("no go.mod and stdin is not a TTY. Pass --module=<path>, set GO_MODULE_PATH, or add --yes to accept inferred value: %s", inferred)
	}
	return inferred, nil
}

func resolveVanityRoot(options bootstrapOptions) string {
	if strings.TrimSpace(options.vanityRoot) != "" {
		return strings.TrimSpace(options.vanityRoot)
	}
	if vanityRoot := strings.TrimSpace(os.Getenv("GO_VANITY_ROOT")); vanityRoot != "" {
		return vanityRoot
	}
	return defaultBootstrapVanityRoot
}

func inferModulePath(vanityRoot string) (string, error) {
	if isInsideGitWorkTree() {
		remote, err := gitOutput("config", "--get", "remote.origin.url")
		if err == nil {
			normalized := normalizeGitRemote(remote)
			if normalized != "" {
				return normalized, nil
			}
		}
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	baseName := filepath.Base(workingDirectory)
	if vanityRoot != "" && baseName != "" && baseName != string(filepath.Separator) {
		return vanityRoot + "/" + baseName, nil
	}
	return "", nil
}

func normalizeGitRemote(remoteURL string) string {
	remoteURL = strings.TrimSpace(remoteURL)
	if strings.HasPrefix(remoteURL, "git@") && strings.Contains(remoteURL, ":") {
		pathPart := strings.TrimPrefix(remoteURL, "git@")
		pathPart = strings.Replace(pathPart, ":", "/", 1)
		return strings.TrimSuffix(strings.TrimSuffix(pathPart, "/"), ".git")
	}
	if strings.HasPrefix(remoteURL, "http://") ||
		strings.HasPrefix(remoteURL, "https://") ||
		strings.HasPrefix(remoteURL, "ssh://") {
		pathPart := remoteURL
		if schemeIndex := strings.Index(pathPart, "://"); schemeIndex >= 0 {
			pathPart = pathPart[schemeIndex+len("://"):]
		}
		if userIndex := strings.Index(pathPart, "@"); userIndex >= 0 {
			pathPart = pathPart[userIndex+1:]
		}
		return strings.TrimSuffix(strings.TrimSuffix(pathPart, "/"), ".git")
	}
	return ""
}

func isInsideGitWorkTree() bool {
	slog.Info("bootstrap inspect git work tree")
	command := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return false
	}
	return true
}

func promptWithDefault(question string, defaultValue string, stdin *os.File, stderr io.Writer) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err == nil {
		defer func() { _ = tty.Close() }()
		fmt.Fprintf(tty, "%s [%s]: ", question, defaultValue)
		answer, readErr := bufio.NewReader(tty).ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return "", readErr
		}
		return defaultedAnswer(answer, defaultValue), nil
	}
	fmt.Fprintf(stderr, "%s [%s]: ", question, defaultValue)
	answer, readErr := bufio.NewReader(stdin).ReadString('\n')
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", readErr
	}
	return defaultedAnswer(answer, defaultValue), nil
}

func defaultedAnswer(answer string, defaultValue string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return defaultValue
	}
	return answer
}

func readModulePath(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errors.New("could not read module from go.mod")
}

func resolveBootstrapLayout(options bootstrapOptions) (string, error) {
	if options.forceLibrary {
		return "library", nil
	}
	if options.forceBinary {
		return "binary", nil
	}
	directories, err := immediateSubdirectories("cmd")
	if err != nil {
		return "", err
	}
	if len(directories) > 0 {
		return "binary", nil
	}
	return "library", nil
}

func buildBootstrapContext(modulePath string, layout string) (bootstrapContext, error) {
	context := bootstrapContext{
		Layout:  layout,
		BaseURL: "https://raw.githubusercontent.com/agoodkind/go-makefile/main",
	}
	if layout != "binary" {
		return context, nil
	}
	binaryName := path.Base(modulePath)
	cmdPath := "./cmd/" + binaryName
	directories, err := immediateSubdirectories("cmd")
	if err != nil {
		return bootstrapContext{}, err
	}
	if directoryExists(filepath.Join("cmd", binaryName)) {
		cmdPath = "./cmd/" + binaryName
	} else if len(directories) == 1 {
		binaryName = directories[0]
		cmdPath = "./cmd/" + directories[0]
	}
	context.Binary = binaryName
	context.Cmd = cmdPath
	if directoryExists(filepath.Join("internal", "version")) {
		context.Vpkg = modulePath + "/internal/version"
	}
	return context, nil
}

func printBootstrapSummary(writer io.Writer, modulePath string, context bootstrapContext) {
	fmt.Fprintf(writer, "module:  %s\n", modulePath)
	fmt.Fprintf(writer, "layout:  %s\n", context.Layout)
	if context.Layout == "binary" {
		fmt.Fprintf(writer, "binary:  %s\n", context.Binary)
		fmt.Fprintf(writer, "cmd:     %s\n", context.Cmd)
	}
	fmt.Fprintln(writer)
}

func reconcileMakefile(context bootstrapContext, stdout io.Writer, stderr io.Writer) error {
	slog.Info("bootstrap reconcile Makefile", slog.String("layout", context.Layout))
	rendered, err := renderMakefile(context)
	if err != nil {
		return err
	}
	if !fileExists("Makefile") {
		if err := os.WriteFile("Makefile", []byte(rendered), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "created Makefile (%s)\n", context.Layout)
		return nil
	}
	existingBytes, err := os.ReadFile("Makefile")
	if err != nil {
		return err
	}
	if string(existingBytes) == rendered {
		fmt.Fprintln(stdout, "skipping Makefile (already current)")
		return nil
	}
	if isClearlyGeneratedMakefile(string(existingBytes)) {
		if err := os.WriteFile("Makefile", []byte(rendered), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "updated Makefile (%s)\n", context.Layout)
		return nil
	}
	absolutePath, err := filepath.Abs("Makefile")
	if err != nil {
		absolutePath = "Makefile"
	}
	fmt.Fprintf(stderr, "warning: %s exists and appears customized; leaving it unchanged\n", absolutePath)
	return nil
}

func renderMakefile(context bootstrapContext) (string, error) {
	templateBytes, err := bootstrapAssetFS.ReadFile(makefileAssetPath)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New("Makefile.tmpl").
		Delims("[[", "]]").
		Option("missingkey=error").
		Parse(string(templateBytes))
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, context); err != nil {
		return "", err
	}
	return output.String(), nil
}

func isClearlyGeneratedMakefile(content string) bool {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, generatedMakefileFirstLine) {
		return false
	}
	sawManagedLine := false
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "include bootstrap.mk" || line == ".DEFAULT_GOAL := check" {
			sawManagedLine = true
			continue
		}
		if !isGeneratedAssignment(line) {
			return false
		}
		sawManagedLine = true
	}
	return sawManagedLine
}

func isGeneratedAssignment(line string) bool {
	key, _, ok := strings.Cut(line, ":=")
	if !ok {
		return false
	}
	switch generatedMakefileKey(strings.TrimSpace(key)) {
	case generatedKeyBinary,
		generatedKeyCmd,
		generatedKeyVpkg,
		generatedKeyLibrary,
		generatedKeyGoMkModules:
		return true
	default:
		return false
	}
}

func reconcileBootstrapMk(stdout io.Writer) error {
	slog.Info("bootstrap reconcile bootstrap.mk")
	contents, err := bootstrapAssetFS.ReadFile(bootstrapMkAssetPath)
	if err != nil {
		return err
	}
	if !fileExists("bootstrap.mk") {
		if err := os.WriteFile("bootstrap.mk", contents, 0o644); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "created bootstrap.mk")
		return nil
	}
	existing, err := os.ReadFile("bootstrap.mk")
	if err != nil {
		return err
	}
	if bytes.Equal(existing, contents) {
		fmt.Fprintln(stdout, "skipping bootstrap.mk (already current)")
		return nil
	}
	if err := os.WriteFile("bootstrap.mk", contents, 0o644); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "updated bootstrap.mk")
	return nil
}

func warnIfLocalGolangCI(stderr io.Writer) {
	slog.Info("bootstrap inspect golangci config")
	if fileExists(".golangci.yml") {
		fmt.Fprintln(stderr, "warning: .golangci.yml exists in project root. The central go-makefile/golangci.yml fetched into .make/golangci.yml is the canonical config; the per-repo file is ignored by GOLANGCI_LINT_FLAGS. Remove it or move overrides upstream into go-makefile.")
	}
}

func reconcileGitignore(stdout io.Writer) error {
	slog.Info("bootstrap reconcile gitignore")
	const makeEntry = ".make/"
	if !fileExists(".gitignore") {
		if err := os.WriteFile(".gitignore", []byte(makeEntry+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "created .gitignore")
		return nil
	}
	contents, err := os.ReadFile(".gitignore")
	if err != nil {
		return err
	}
	if hasLine(string(contents), makeEntry) {
		return nil
	}
	var builder strings.Builder
	builder.Write(contents)
	if len(contents) > 0 && !strings.HasSuffix(string(contents), "\n") {
		builder.WriteString("\n")
	}
	builder.WriteString(makeEntry)
	builder.WriteString("\n")
	if err := os.WriteFile(".gitignore", []byte(builder.String()), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "added .make/ to .gitignore")
	return nil
}

func hasLine(contents string, expected string) bool {
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == expected {
			return true
		}
	}
	return false
}

func printBootstrapDone(writer io.Writer) {
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "done.")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Lint and build are centralized in go-makefile. The canonical entry points are:")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "  make build   vet + full lint chain + govulncheck, then go build")
	fmt.Fprintln(writer, "  make check   build + test")
	fmt.Fprintln(writer, "  make lint    just the full lint chain")
	fmt.Fprintln(writer, "  make fmt     apply gofumpt + goimports")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Run 'make help' for the full target list, including per-linter sub-targets")
	fmt.Fprintln(writer, "and baseline-refresh targets. Do not add project-local lint, deadcode, audit,")
	fmt.Fprintln(writer, "or staticcheck targets; doing so splits enforcement and lets agents bypass")
	fmt.Fprintln(writer, "the central rules.")
}

func immediateSubdirectories(directoryPath string) ([]string, error) {
	entries, err := os.ReadDir(directoryPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	directories := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			directories = append(directories, entry.Name())
		}
	}
	return directories, nil
}

func directoryExists(directoryPath string) bool {
	info, err := os.Stat(directoryPath)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func fileExists(filePath string) bool {
	info, err := os.Stat(filePath)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func runBootstrapProcess(name string, args []string, stdout io.Writer, stderr io.Writer) error {
	slog.Info("bootstrap run process", slog.String("command", name), slog.Int("args", len(args)))
	command := exec.Command(name, args...)
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}
