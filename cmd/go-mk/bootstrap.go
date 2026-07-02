package main

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
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

//go:embed bootstrap_assets/bootstrap.mk bootstrap_assets/Makefile.tmpl bootstrap_assets/ci.yml bootstrap_assets/release.yml
var bootstrapAssetFS embed.FS

const (
	defaultBootstrapVanityRoot = "goodkind.io"
	makefileAssetPath          = "bootstrap_assets/Makefile.tmpl"
	bootstrapMkAssetPath       = "bootstrap_assets/bootstrap.mk"
	ciWorkflowAssetPath        = "bootstrap_assets/ci.yml"
	ciWorkflowPath             = ".github/workflows/ci.yml"
	releaseWorkflowAssetPath   = "bootstrap_assets/release.yml"
	releaseWorkflowPath        = ".github/workflows/release.yml"
	releaseReusableWorkflowRef = "agoodkind/go-makefile/.github/workflows/_release.yml"
	ciReusableWorkflowRef      = "agoodkind/go-makefile/.github/workflows/_ci.yml"
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
	futureTrackedFiles := bootstrapManagedTrackedFiles(options.stderr)
	printBootstrapSummary(options.stdout, modulePath, context)
	if err := reconcileMakefile(context, options.stdout, options.stderr); err != nil {
		return err
	}
	if err := reconcileBootstrapMk(options.stdout); err != nil {
		return err
	}
	if err := reconcileCIWorkflow(options.stdout); err != nil {
		return err
	}
	if err := reconcileReleaseWorkflow(options.stdout); err != nil {
		return err
	}
	warnIfLocalGolangCI(options.stderr)
	if err := reconcileGitignore(futureTrackedFiles, options.stdout); err != nil {
		return err
	}
	if err := reconcileGoModTools(options.stdout); err != nil {
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
		remote, err := bootstrapGitOutput("config", "--get", "remote.origin.url")
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
		if len(fields) >= 2 && fields[0] == "module" {
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

// reconcileCIWorkflow owns the consumer's CI caller so the CI-permissions drift
// cannot recur. When no ci.yml exists it scaffolds the canonical caller. When a
// ci.yml already calls the go-makefile reusable CI workflow it repairs the caller
// job's permissions block in place (adding id-token and attestations write for
// the reusable release-build path) and adds secrets: inherit so the signing
// secrets reach that build, while preserving the uses line, with inputs,
// comments, and formatting. A ci.yml that does not reference the reusable
// workflow is left untouched, matching the custom-workflow rule for release.yml.
func reconcileCIWorkflow(stdout io.Writer) error {
	slog.Info("bootstrap reconcile ci workflow")
	if !fileExists(ciWorkflowPath) {
		contents, err := bootstrapAssetFS.ReadFile(ciWorkflowAssetPath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(ciWorkflowPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(ciWorkflowPath, contents, 0o644); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "created .github/workflows/ci.yml")
		return nil
	}
	existing, err := os.ReadFile(ciWorkflowPath)
	if err != nil {
		return err
	}
	if !strings.Contains(string(existing), ciReusableWorkflowRef) {
		fmt.Fprintln(stdout, "skipping .github/workflows/ci.yml (exists; leaving consumer workflow unchanged)")
		return nil
	}
	repaired, permsChanged := repairCallerPermissions(string(existing), ciReusableWorkflowRef, ciCallerPermissions)
	repaired, secretsChanged := ensureSecretsInherit(repaired, ciReusableWorkflowRef)
	if !permsChanged && !secretsChanged {
		fmt.Fprintln(stdout, "skipping .github/workflows/ci.yml (already current)")
		return nil
	}
	if err := os.WriteFile(ciWorkflowPath, []byte(repaired), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "updated .github/workflows/ci.yml")
	return nil
}

// workflowPermission is a single permissions-block entry (a scope key and its
// grant), used to insert or repair a reusable-workflow caller's permissions.
type workflowPermission struct {
	key   string
	value string
}

// requiredReleasePermissions are the permission keys the reusable release
// workflow's attestation steps need. Consumers that granted only contents:write
// failed at startup because the id-token and attestations grants were missing,
// so bootstrap repairs the release caller to grant all three.
var requiredReleasePermissions = []workflowPermission{
	{"contents", "write"},
	{"id-token", "write"},
	{"attestations", "write"},
}

// ciCallerPermissions are the permission keys the reusable CI workflow's
// release-build path needs from its caller: repo read for checkout, an OIDC
// token, and attestation write. A CI caller is purely the reusable-CI job, so
// contents:read is the correct least-privilege grant, and repairing to these
// exact values also ensures contents is present (a permissions block that omits
// it would otherwise leave contents at none and break checkout).
var ciCallerPermissions = []workflowPermission{
	{"contents", "read"},
	{"id-token", "write"},
	{"attestations", "write"},
}

// reconcileReleaseWorkflow owns the consumer's release caller so the
// release-permissions drift cannot recur. When no release.yml exists it
// scaffolds the canonical caller for every repo. When a release.yml already
// calls the go-makefile reusable release workflow it repairs the release job's
// permissions block in place, adding any missing required key while preserving
// the uses line, the with inputs, secrets: inherit, comments, and formatting. A
// release.yml that does not reference the reusable workflow is left untouched,
// matching the custom-workflow rule for ci.yml.
func reconcileReleaseWorkflow(stdout io.Writer) error {
	slog.Info("bootstrap reconcile release workflow")
	if !fileExists(releaseWorkflowPath) {
		contents, err := bootstrapAssetFS.ReadFile(releaseWorkflowAssetPath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(releaseWorkflowPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(releaseWorkflowPath, contents, 0o644); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "created .github/workflows/release.yml")
		return nil
	}
	existing, err := os.ReadFile(releaseWorkflowPath)
	if err != nil {
		return err
	}
	if !strings.Contains(string(existing), releaseReusableWorkflowRef) {
		fmt.Fprintln(stdout, "skipping .github/workflows/release.yml (exists; leaving consumer workflow unchanged)")
		return nil
	}
	repaired, changed := repairReleasePermissions(string(existing))
	if !changed {
		fmt.Fprintln(stdout, "skipping .github/workflows/release.yml (already current)")
		return nil
	}
	if err := os.WriteFile(releaseWorkflowPath, []byte(repaired), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "updated .github/workflows/release.yml")
	return nil
}

// repairReleasePermissions repairs the release caller's permissions block using
// the release workflow's required grant.
func repairReleasePermissions(content string) (string, bool) {
	return repairCallerPermissions(content, releaseReusableWorkflowRef, requiredReleasePermissions)
}

// repairCallerPermissions performs a surgical, formatting-preserving edit of a
// reusable-workflow caller: it finds the job whose uses line references usesRef,
// locates that job's permissions key, and repairs the block-form permissions
// mapping by inserting any missing permissions key and rewriting any present
// permissions key whose value is not the required grant. A job with no
// permissions key gets the whole block inserted after its uses line. A job that
// already declares permissions in the scalar form (for example
// permissions: write-all) or the inline-map form (for example
// permissions: { contents: write }) is left untouched, since a scalar grant such
// as write-all already covers every scope and surgically editing a non-block
// form risks corrupting the mapping. Every other byte is left identical. The
// second return value reports whether any key was inserted or rewritten.
func repairCallerPermissions(content string, usesRef string, permissions []workflowPermission) (string, bool) {
	lineEnding := "\n"
	if strings.Contains(content, "\r\n") {
		lineEnding = "\r\n"
	}
	lines := strings.Split(content, lineEnding)
	usesIndex := usesLineIndex(lines, usesRef)
	if usesIndex < 0 {
		return content, false
	}
	usesIndent := leadingSpaceCount(lines[usesIndex])
	jobHeaderIndex := jobHeaderIndexAbove(lines, usesIndex, usesIndent)
	if jobHeaderIndex < 0 {
		return content, false
	}
	jobIndent := leadingSpaceCount(lines[jobHeaderIndex])
	jobEnd := jobBodyEndIndex(lines, usesIndex, jobIndent)
	permissionsIndex := permissionsLineIndex(lines, jobHeaderIndex+1, jobEnd, usesIndent)
	if permissionsIndex < 0 {
		return insertPermissionsBlock(lines, usesIndex, usesIndent, usesIndent-jobIndent, lineEnding, permissions), true
	}
	if !isBlockFormPermissions(lines[permissionsIndex]) {
		return content, false
	}
	return insertMissingPermissions(lines, permissionsIndex, jobEnd, usesIndent, lineEnding, permissions)
}

func usesLineIndex(lines []string, usesRef string) int {
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "uses:") && strings.Contains(trimmed, usesRef) {
			return index
		}
	}
	return -1
}

func jobHeaderIndexAbove(lines []string, fromIndex int, childIndent int) int {
	for index := fromIndex - 1; index >= 0; index-- {
		if isBlankOrCommentLine(lines[index]) {
			continue
		}
		if leadingSpaceCount(lines[index]) < childIndent {
			return index
		}
	}
	return -1
}

func jobBodyEndIndex(lines []string, fromIndex int, jobIndent int) int {
	for index := fromIndex + 1; index < len(lines); index++ {
		if isBlankOrCommentLine(lines[index]) {
			continue
		}
		if leadingSpaceCount(lines[index]) <= jobIndent {
			return index
		}
	}
	return len(lines)
}

func permissionsLineIndex(lines []string, startIndex int, endIndex int, jobChildIndent int) int {
	for index := startIndex; index < endIndex; index++ {
		if leadingSpaceCount(lines[index]) != jobChildIndent {
			continue
		}
		key, _, found := strings.Cut(strings.TrimSpace(lines[index]), ":")
		if found && strings.TrimSpace(key) == "permissions" {
			return index
		}
	}
	return -1
}

// isBlockFormPermissions reports whether a permissions key line is the block
// form (permissions: with no inline value), as opposed to the scalar form
// (permissions: write-all) or the inline-map form (permissions: { ... }). Only
// the block form is safe to repair in place by adding or rewriting child keys.
func isBlockFormPermissions(line string) bool {
	_, value, _ := strings.Cut(strings.TrimSpace(line), ":")
	return strings.TrimSpace(value) == ""
}

func insertPermissionsBlock(lines []string, usesIndex int, jobChildIndent int, indentStep int, lineEnding string, permissions []workflowPermission) string {
	childIndent := jobChildIndent + indentStep
	block := []string{strings.Repeat(" ", jobChildIndent) + "permissions:"}
	for _, permission := range permissions {
		block = append(block, strings.Repeat(" ", childIndent)+permission.key+": "+permission.value)
	}
	updated := make([]string, 0, len(lines)+len(block))
	updated = append(updated, lines[:usesIndex+1]...)
	updated = append(updated, block...)
	updated = append(updated, lines[usesIndex+1:]...)
	return strings.Join(updated, lineEnding)
}

func insertMissingPermissions(lines []string, permissionsIndex int, jobEnd int, jobChildIndent int, lineEnding string, permissions []workflowPermission) (string, bool) {
	presentIndex := map[string]int{}
	childIndent := -1
	lastChildIndex := permissionsIndex
	for index := permissionsIndex + 1; index < jobEnd; index++ {
		if isBlankOrCommentLine(lines[index]) {
			continue
		}
		indent := leadingSpaceCount(lines[index])
		if indent <= jobChildIndent {
			break
		}
		if childIndent < 0 {
			childIndent = indent
		}
		key, _, _ := strings.Cut(strings.TrimSpace(lines[index]), ":")
		presentIndex[strings.TrimSpace(key)] = index
		lastChildIndex = index
	}
	if childIndent < 0 {
		childIndent = jobChildIndent + 2
	}
	updated := append([]string(nil), lines...)
	changed := false
	for _, permission := range permissions {
		index, ok := presentIndex[permission.key]
		if !ok {
			continue
		}
		corrected := strings.Repeat(" ", leadingSpaceCount(updated[index])) + permission.key + ": " + permission.value
		if updated[index] != corrected {
			updated[index] = corrected
			changed = true
		}
	}
	insertions := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		if _, ok := presentIndex[permission.key]; !ok {
			insertions = append(insertions, strings.Repeat(" ", childIndent)+permission.key+": "+permission.value)
		}
	}
	if len(insertions) == 0 {
		return strings.Join(updated, lineEnding), changed
	}
	result := make([]string, 0, len(updated)+len(insertions))
	result = append(result, updated[:lastChildIndex+1]...)
	result = append(result, insertions...)
	result = append(result, updated[lastChildIndex+1:]...)
	return strings.Join(result, lineEnding), true
}

// ensureSecretsInherit adds a job-level `secrets: inherit` to the reusable-CI
// caller job so the consumer's signing secrets reach the reusable workflow's
// release-build path. It finds the job whose uses line references usesRef and, if
// that job body has no `secrets:` key at the job-child indentation, inserts
// `secrets: inherit` right after the uses line. A job that already declares a
// `secrets:` key (inherit or an explicit mapping) is left untouched. The second
// return value reports whether the line was inserted.
func ensureSecretsInherit(content string, usesRef string) (string, bool) {
	lineEnding := "\n"
	if strings.Contains(content, "\r\n") {
		lineEnding = "\r\n"
	}
	lines := strings.Split(content, lineEnding)
	usesIndex := usesLineIndex(lines, usesRef)
	if usesIndex < 0 {
		return content, false
	}
	usesIndent := leadingSpaceCount(lines[usesIndex])
	jobHeaderIndex := jobHeaderIndexAbove(lines, usesIndex, usesIndent)
	if jobHeaderIndex < 0 {
		return content, false
	}
	jobIndent := leadingSpaceCount(lines[jobHeaderIndex])
	jobEnd := jobBodyEndIndex(lines, usesIndex, jobIndent)
	for index := jobHeaderIndex + 1; index < jobEnd; index++ {
		if leadingSpaceCount(lines[index]) != usesIndent {
			continue
		}
		key, _, found := strings.Cut(strings.TrimSpace(lines[index]), ":")
		if found && strings.TrimSpace(key) == "secrets" {
			return content, false
		}
	}
	// Insert after the uses line and any contiguous comment or blank lines that
	// document it, so a comment block stays attached to uses rather than being
	// split from it.
	insertAt := usesIndex + 1
	for insertAt < jobEnd && isBlankOrCommentLine(lines[insertAt]) {
		insertAt++
	}
	secretsLine := strings.Repeat(" ", usesIndent) + "secrets: inherit"
	updated := make([]string, 0, len(lines)+1)
	updated = append(updated, lines[:insertAt]...)
	updated = append(updated, secretsLine)
	updated = append(updated, lines[insertAt:]...)
	return strings.Join(updated, lineEnding), true
}

func leadingSpaceCount(line string) int {
	count := 0
	for _, character := range line {
		if character != ' ' {
			break
		}
		count++
	}
	return count
}

func isBlankOrCommentLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "" || strings.HasPrefix(trimmed, "#")
}

func configuredBaselineFiles() []string {
	return []string{
		lintEnvDefault("GOLANGCI_LINT_BASELINE", ".golangci-lint-baseline.txt"),
		lintEnvDefault("GOCYCLO_BASELINE", ".gocyclo-baseline.txt"),
		lintEnvDefault("DEADCODE_BASELINE", ".deadcode-baseline.txt"),
		lintEnvDefault("STATICCHECK_EXTRA_BASELINE", ".staticcheck-extra-baseline.txt"),
	}
}

func configuredAppliedNoticesFile() string {
	return lintEnvDefault("GO_MK_APPLIED_NOTICES", ".go-mk-applied-notices")
}

func configuredBootstrapTrackedFiles() []string {
	trackedFiles := append([]string{}, configuredBaselineFiles()...)
	trackedFiles = append(trackedFiles, configuredAppliedNoticesFile())
	return trackedFiles
}

func bootstrapManagedTrackedFiles(stderr io.Writer) []string {
	managedFiles := make([]string, 0, len(configuredBootstrapTrackedFiles()))
	seen := make(map[string]bool, len(configuredBootstrapTrackedFiles()))
	for _, trackedFile := range configuredBootstrapTrackedFiles() {
		managedPath, ok := sanitizeBootstrapManagedPath(trackedFile)
		if !ok {
			fmt.Fprintf(stderr, "warning: bootstrap skips non-repo tracked file %s; keep it tracked manually\n", trackedFile)
			continue
		}
		if seen[managedPath] {
			continue
		}
		seen[managedPath] = true
		managedFiles = append(managedFiles, managedPath)
	}
	return managedFiles
}

func sanitizeBootstrapManagedPath(filePath string) (string, bool) {
	trimmedPath := strings.TrimSpace(filePath)
	if trimmedPath == "" {
		return "", false
	}
	cleanPath := filepath.Clean(trimmedPath)
	if filepath.IsAbs(cleanPath) {
		return "", false
	}
	if cleanPath == "." || cleanPath == ".." {
		return "", false
	}
	if strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		return "", false
	}
	normalizedPath := path.Clean(filepath.ToSlash(cleanPath))
	if normalizedPath == "." || normalizedPath == ".." || strings.HasPrefix(normalizedPath, "../") {
		return "", false
	}
	return normalizedPath, true
}

func warnIfLocalGolangCI(stderr io.Writer) {
	slog.Info("bootstrap inspect golangci config")
	if fileExists(".golangci.yml") {
		fmt.Fprintln(stderr, "warning: .golangci.yml exists in project root. The central go-makefile/golangci.yml fetched into .make/golangci.yml is the canonical config; the per-repo file is ignored by GOLANGCI_LINT_FLAGS. Remove it or move overrides upstream into go-makefile.")
	}
}

func reconcileGitignore(trackedFiles []string, stdout io.Writer) error {
	slog.Info("bootstrap reconcile gitignore")
	managedEntries := bootstrapGitignoreEntries(trackedFiles)
	if !fileExists(".gitignore") {
		if err := os.WriteFile(".gitignore", []byte(strings.Join(managedEntries, "\n")+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "created .gitignore")
		return nil
	}
	contents, err := os.ReadFile(".gitignore")
	if err != nil {
		return err
	}
	missingEntries := missingGitignoreEntries(string(contents), managedEntries)
	if len(missingEntries) == 0 {
		return nil
	}
	var builder strings.Builder
	builder.Write(contents)
	if len(contents) > 0 && !strings.HasSuffix(string(contents), "\n") {
		builder.WriteString("\n")
	}
	for _, entry := range missingEntries {
		builder.WriteString(entry)
		builder.WriteString("\n")
	}
	if err := os.WriteFile(".gitignore", []byte(builder.String()), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "updated .gitignore")
	return nil
}

// goMakefileManagedTools are the tool packages go-makefile installs itself via
// `go install`, with versions controlled by go.mk's *_INSTALL variables. A
// consumer must
// not also pin them with a go.mod `tool` directive: that pulls each tool's large
// transitive graph into the module (for example golangci-lint -> viper, which
// drags in the pre-split google.golang.org/genproto and then collides with the
// split genproto modules in a go.work build). Bootstrap removes these directives
// so they cannot accumulate; project-specific tools (buf, mockgen, and the like)
// are left untouched.
var goMakefileManagedTools = []string{
	"github.com/golangci/golangci-lint/v2/cmd/golangci-lint",
	"github.com/fzipp/gocyclo/cmd/gocyclo",
	"golang.org/x/tools/cmd/deadcode",
	"golang.org/x/tools/cmd/goimports",
	"golang.org/x/vuln/cmd/govulncheck",
	"mvdan.cc/gofumpt",
	"goodkind.io/go-makefile/staticcheck/cmd/staticcheck-extra",
}

func reconcileGoModTools(stdout io.Writer) error {
	slog.Info("bootstrap reconcile go.mod tool directives")
	if !fileExists("go.mod") {
		return nil
	}
	declared, err := goModToolDirectives()
	if err != nil {
		return err
	}
	managed := make(map[string]bool, len(goMakefileManagedTools))
	for _, toolPath := range goMakefileManagedTools {
		managed[toolPath] = true
	}
	dropArgs := []string{"mod", "edit"}
	droppedCount := 0
	for _, toolPath := range declared {
		if !managed[toolPath] {
			continue
		}
		dropArgs = append(dropArgs, "-droptool="+toolPath)
		droppedCount++
	}
	if droppedCount == 0 {
		return nil
	}
	if err := exec.Command("go", dropArgs...).Run(); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "removed %d go-makefile-managed tool directive(s) from go.mod; run `go mod tidy` to prune their dependencies\n", droppedCount)
	return nil
}

// goModToolDirectives returns the tool directive paths declared in the current
// module's go.mod, read via `go mod edit -json`.
func goModToolDirectives() ([]string, error) {
	slog.Info("bootstrap read go.mod tool directives")
	output, err := exec.Command("go", "mod", "edit", "-json").Output()
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Tool []struct {
			Path string `json:"Path"`
		} `json:"Tool"`
	}
	if err := json.Unmarshal(output, &parsed); err != nil {
		return nil, err
	}
	toolPaths := make([]string, 0, len(parsed.Tool))
	for _, tool := range parsed.Tool {
		toolPaths = append(toolPaths, tool.Path)
	}
	return toolPaths, nil
}

func bootstrapGitignoreEntries(trackedFiles []string) []string {
	// go.work stays untracked: it is per-developer, and go.mk regenerates it on
	// demand from GO_MK_WORKSPACE_USE for routing the module proxy cannot resolve
	// on its own, so the workspace file is never committed.
	entries := []string{".make/", "go.work", "go.work.sum"}
	seen := map[string]bool{
		".make/":      true,
		"go.work":     true,
		"go.work.sum": true,
	}
	for _, trackedFile := range trackedFiles {
		for _, entry := range gitignoreAllowlistEntries(trackedFile) {
			if seen[entry] {
				continue
			}
			seen[entry] = true
			entries = append(entries, entry)
		}
	}
	return entries
}

func gitignoreAllowlistEntries(filePath string) []string {
	pathParts := strings.Split(path.Clean(filePath), "/")
	entries := make([]string, 0, len(pathParts))
	currentPath := ""
	for index, pathPart := range pathParts {
		if currentPath == "" {
			currentPath = pathPart
		} else {
			currentPath += "/" + pathPart
		}
		if index < len(pathParts)-1 {
			entries = append(entries, "!"+currentPath+"/")
			continue
		}
		entries = append(entries, "!"+currentPath)
	}
	return entries
}

func missingGitignoreEntries(contents string, entries []string) []string {
	missingEntries := make([]string, 0, len(entries))
	for _, entry := range entries {
		if hasLine(contents, entry) {
			continue
		}
		missingEntries = append(missingEntries, entry)
	}
	return missingEntries
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
	fmt.Fprintln(writer, "Commit Makefile, bootstrap.mk, .github/workflows/ci.yml, and .gitignore.")
	fmt.Fprintln(writer, "Commit baseline files or .go-mk-applied-notices only after a baseline or notice run creates them.")
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

func bootstrapGitOutput(args ...string) (string, error) {
	return loggedGitOutput("bootstrap git", args...)
}
