package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// baseCIChangedConfig returns a push-event config whose seams default to a
// resolvable base, an empty diff, no source files, and no submodules. Each test
// overrides only the fields it exercises.
func baseCIChangedConfig() ciChangedConfig {
	return ciChangedConfig{
		eventName:     "push",
		base:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		head:          "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		defaultBranch: "main",
		refName:       "main",
		baseInHistory: func(string) bool { return true },
		mergeBase:     func(_, _ string) (string, bool) { return "", false },
		diffNames:     func(_, _ string) ([]string, error) { return nil, nil },
		sourceFiles:   func() ([]string, error) { return nil, nil },
		submoduleDirs: func() ([]string, error) { return nil, nil },
		stdout:        func(string) {},
	}
}

// runCIChangedCapture runs the detector with stdout and GITHUB_OUTPUT redirected
// to a buffer and a temp file, returning the status, the printed text, and the
// output-file contents.
func runCIChangedCapture(t *testing.T, config ciChangedConfig) (int, string, string) {
	t.Helper()
	var out strings.Builder
	config.stdout = func(text string) { out.WriteString(text) }
	outputFile := filepath.Join(t.TempDir(), "github_output.txt")
	config.outputPath = outputFile
	status := runCIChangedWith(config)
	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	return status, out.String(), string(data)
}

func TestDecideChanged(t *testing.T) {
	cases := []struct {
		name    string
		inputs  ciChangeInputs
		changed bool
	}{
		{
			name: "docs only skips",
			inputs: ciChangeInputs{
				changedPaths: []string{"README.md", "docs/guide.md"},
				sourceFiles:  []string{"cmd/go-mk/main.go"},
			},
			changed: false,
		},
		{
			name: "declared codegen input dir runs",
			inputs: ciChangeInputs{
				changedPaths: []string{"shim/build.sh"},
				generateDirs: []string{"shim", "injector", "api"},
			},
			changed: true,
		},
		{
			name: "go source in build graph runs",
			inputs: ciChangeInputs{
				changedPaths: []string{"cmd/go-mk/main.go"},
				sourceFiles:  []string{"cmd/go-mk/main.go"},
			},
			changed: true,
		},
		{
			name: "embedded file in build graph runs",
			inputs: ciChangeInputs{
				changedPaths: []string{"templates/page.tmpl"},
				sourceFiles:  []string{"templates/page.tmpl"},
			},
			changed: true,
		},
		{
			name: "cgo c file runs by extension",
			inputs: ciChangeInputs{
				changedPaths: []string{"internal/scan/match.c"},
				sourceFiles:  nil,
			},
			changed: true,
		},
		{
			name: "deleted go file runs by extension",
			inputs: ciChangeInputs{
				changedPaths: []string{"cmd/go-mk/removed.go"},
				sourceFiles:  []string{"cmd/go-mk/main.go"},
			},
			changed: true,
		},
		{
			name: "go mod runs",
			inputs: ciChangeInputs{
				changedPaths: []string{"go.mod"},
			},
			changed: true,
		},
		{
			name: "workflow runs",
			inputs: ciChangeInputs{
				changedPaths: []string{".github/workflows/_ci.yml"},
			},
			changed: true,
		},
		{
			name: "submodule pointer runs",
			inputs: ciChangeInputs{
				changedPaths:  []string{"third_party/gksyntax"},
				submoduleDirs: []string{"third_party/gksyntax"},
			},
			changed: true,
		},
		{
			name: "workspace grammar source runs",
			inputs: ciChangeInputs{
				changedPaths:  []string{"third_party/gksyntax/grammar.js"},
				workspaceDirs: []string{"third_party/gksyntax"},
			},
			changed: true,
		},
		{
			name: "subdir module go.mod runs",
			inputs: ciChangeInputs{
				changedPaths: []string{"dots/go.mod"},
			},
			changed: true,
		},
		{
			name: "second module go.mod runs",
			inputs: ciChangeInputs{
				changedPaths: []string{"staticcheck/go.mod"},
			},
			changed: true,
		},
		{
			name: "objective-c source runs",
			inputs: ciChangeInputs{
				changedPaths: []string{"internal/cam/capture.m"},
			},
			changed: true,
		},
		{
			name: "prebuilt syso runs",
			inputs: ciChangeInputs{
				changedPaths: []string{"internal/rsrc/icon.syso"},
			},
			changed: true,
		},
		{
			name: "readme alone skips",
			inputs: ciChangeInputs{
				changedPaths: []string{"README.md"},
			},
			changed: false,
		},
		{
			name: "baseline file runs",
			inputs: ciChangeInputs{
				changedPaths: []string{".golangci-lint-baseline.txt"},
			},
			changed: true,
		},
		{
			name: "fetched go.mk under .make runs",
			inputs: ciChangeInputs{
				changedPaths: []string{".make/go.mk"},
			},
			changed: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			changed, reason := decideChanged(testCase.inputs)
			if changed != testCase.changed {
				t.Fatalf("decideChanged = %v (%s), want %v", changed, reason, testCase.changed)
			}
		})
	}
}

func TestRunCIChangedFailsSafeOnNonPushEvent(t *testing.T) {
	fixture := ciGitFixtureNew(t)
	ciGitFixtureWrite(t, fixture.root, "docs/guide.md", "Updated documentation.\n")
	ciGitFixtureCommit(t, fixture.root)
	config := ciGitFixtureConfig(fixture)
	config.eventName = "pull_request"
	ciGitFixtureAssert(t, config, true, "event pull_request is not push")
}

func TestRunCIChangedFailsSafeOnNewBranch(t *testing.T) {
	for _, base := range []string{"", zeroSHA} {
		t.Run("base="+base, func(t *testing.T) {
			fixture := ciGitFixtureNew(t)
			config := ciGitFixtureConfig(fixture)
			config.base = base
			ciGitFixtureAssert(t, config, true, "no base commit (new branch)")
		})
	}
}

func TestRunCIChangedFailsSafeWhenBaseNotAncestor(t *testing.T) {
	fixture := ciGitFixtureNew(t)
	config := ciGitFixtureConfig(fixture)
	config.base = ciGitFixtureDiverge(t, fixture)
	ciGitFixtureAssert(t, config, true, "not an ancestor of head")
}

func TestRunCIChangedExactBaseSkipsAncestryCheck(t *testing.T) {
	fixture := ciGitFixtureNew(t)
	config := ciGitFixtureConfig(fixture)
	config.base = ciGitFixtureDiverge(t, fixture)
	config.baseIsExact = true
	ciGitFixtureAssert(t, config, false, "no Go-relevant changes")
}

func TestRunCIChangedFeatureBranchUsesMergeBaseDiff(t *testing.T) {
	t.Run("earlier feature input change", func(t *testing.T) {
		fixture := ciGitFixtureNew(t)
		ciGitFixtureGit(t, fixture.root, "checkout", "-b", "feature")
		ciGitFixtureWrite(t, fixture.root, "assets/input.txt", "Feature input.\n")
		before := ciGitFixtureCommit(t, fixture.root)
		ciGitFixtureWrite(t, fixture.root, "docs/guide.md", "Feature documentation.\n")
		ciGitFixtureCommit(t, fixture.root)
		config := ciGitFixtureConfig(fixture)
		config.base = before
		config.refName = "feature"
		ciGitFixtureAssert(t, config, true, "Go build input changed: assets/input.txt")
	})
	t.Run("trunk input change excluded", func(t *testing.T) {
		fixture := ciGitFixtureNew(t)
		ciGitFixtureGit(t, fixture.root, "branch", "feature")
		ciGitFixtureWrite(t, fixture.root, "assets/input.txt", "Trunk input.\n")
		ciGitFixtureCommit(t, fixture.root)
		ciGitFixtureGit(t, fixture.root, "push", "origin", "main")
		ciGitFixtureGit(t, fixture.root, "fetch", "origin")
		ciGitFixtureGit(t, fixture.root, "checkout", "feature")
		ciGitFixtureWrite(t, fixture.root, "docs/guide.md", "Feature documentation.\n")
		ciGitFixtureCommit(t, fixture.root)
		config := ciGitFixtureConfig(fixture)
		config.refName = "feature"
		ciGitFixtureAssert(t, config, false, "no Go-relevant changes")
	})
}

func TestRunCIChangedTrunkPushUsesBeforeAfterDiff(t *testing.T) {
	t.Run("earlier input change excluded", func(t *testing.T) {
		fixture := ciGitFixtureNew(t)
		ciGitFixtureWrite(t, fixture.root, "assets/input.txt", "Earlier input.\n")
		before := ciGitFixtureCommit(t, fixture.root)
		ciGitFixtureWrite(t, fixture.root, "docs/guide.md", "Trunk documentation.\n")
		ciGitFixtureCommit(t, fixture.root)
		config := ciGitFixtureConfig(fixture)
		config.base = before
		ciGitFixtureAssert(t, config, false, "no Go-relevant changes")
	})
	t.Run("current embedded input change", func(t *testing.T) {
		fixture := ciGitFixtureNew(t)
		ciGitFixtureWrite(t, fixture.root, "assets/input.txt", "Current input.\n")
		ciGitFixtureCommit(t, fixture.root)
		ciGitFixtureAssert(t, ciGitFixtureConfig(fixture), true, "Go build input changed: assets/input.txt")
	})
}

func TestRunCIChangedFailsSafeWhenMergeBaseMissing(t *testing.T) {
	fixture := ciGitFixtureNew(t)
	ciGitFixtureGit(t, fixture.root, "checkout", "--orphan", "feature")
	ciGitFixtureGit(t, fixture.root, "rm", "-r", "--cached", ".")
	ciGitFixtureWrite(t, fixture.root, "docs/guide.md", "Unrelated documentation.\n")
	ciGitFixtureCommit(t, fixture.root)
	config := ciGitFixtureConfig(fixture)
	config.refName = "feature"
	ciGitFixtureAssert(t, config, true, "cannot compute merge-base with main")
}

func TestRunCIChangedFailsSafeOnSubmoduleError(t *testing.T) {
	fixture := ciGitFixtureNew(t)
	ciGitFixtureWrite(t, fixture.root, "docs/guide.md", "Updated documentation.\n")
	ciGitFixtureCommit(t, fixture.root)
	// The diff excludes .gitmodules to distinguish submodule errors from build configuration changes.
	ciGitFixtureWrite(t, fixture.root, ".gitmodules", "[submodule \"broken\"\n")
	ciGitFixtureAssert(t, ciGitFixtureConfig(fixture), true, "submodule discovery failed")
}

func TestRunCIChangedFailsSafeOnDiffError(t *testing.T) {
	fixture := ciGitFixtureNew(t)
	config := ciGitFixtureConfig(fixture)
	config.base = "missing-revision"
	config.baseIsExact = true
	ciGitFixtureAssert(t, config, true, "git diff failed")
}

func TestRunCIChangedFailsSafeOnGoListError(t *testing.T) {
	fixture := ciGitFixtureNew(t)
	ciGitFixtureWrite(t, fixture.root, "docs/guide.md", "Updated documentation.\n")
	ciGitFixtureCommit(t, fixture.root)
	// The diff excludes go.mod to distinguish go list errors from build configuration changes.
	ciGitFixtureWrite(t, fixture.root, "go.mod", "module example.invalid/ci-fixture\n\ngo invalid\n")
	ciGitFixtureAssert(t, ciGitFixtureConfig(fixture), true, "go list failed")
}

func TestRunCIChangedEmptyDiffSkips(t *testing.T) {
	fixture := ciGitFixtureNew(t)
	ciGitFixtureAssert(t, ciGitFixtureConfig(fixture), false, "diff is empty")
}

func TestRunCIChangedDocsOnlySkips(t *testing.T) {
	config := baseCIChangedConfig()
	config.diffNames = func(_, _ string) ([]string, error) {
		return []string{"README.md", "docs/guide.md"}, nil
	}
	config.sourceFiles = func() ([]string, error) { return []string{"cmd/go-mk/main.go"}, nil }
	status, _, output := runCIChangedCapture(t, config)
	if status != 0 || !strings.Contains(output, "changed=false") {
		t.Fatalf("status=%d output=%q, want 0 and changed=false", status, output)
	}
}

func TestRunCIChangedFailsSafeWhenCodegenDeclaresNoInputs(t *testing.T) {
	fixture := ciGitFixtureNew(t)
	ciGitFixtureWrite(t, fixture.root, "docs/guide.md", "Updated documentation.\n")
	ciGitFixtureCommit(t, fixture.root)
	config := ciGitFixtureConfig(fixture)
	config.generate = "go-generated-prereqs"
	ciGitFixtureAssert(t, config, true, "codegen declares no inputs")
}

func TestRunCIChangedDeclaredCodegenInputsSkipDocsOnly(t *testing.T) {
	fixture := ciGitFixtureNew(t)
	ciGitFixtureWrite(t, fixture.root, "docs/guide.md", "Updated documentation.\n")
	ciGitFixtureCommit(t, fixture.root)
	config := ciGitFixtureConfig(fixture)
	config.generate = "go-generated-prereqs"
	config.generateInputs = "shim injector api"
	ciGitFixtureAssert(t, config, false, "no Go-relevant changes")
}

func TestRunCIChangedDeclaredCodegenInputRuns(t *testing.T) {
	fixture := ciGitFixtureNew(t)
	ciGitFixtureWrite(t, fixture.root, "shim/input.txt", "Codegen input.\n")
	ciGitFixtureCommit(t, fixture.root)
	config := ciGitFixtureConfig(fixture)
	config.generate = "go-generated-prereqs"
	config.generateInputs = "shim injector api"
	ciGitFixtureAssert(t, config, true, "declared codegen input changed: shim/input.txt")
}

func TestRunCIChangedGoChangeRuns(t *testing.T) {
	config := baseCIChangedConfig()
	config.diffNames = func(_, _ string) ([]string, error) {
		return []string{"cmd/go-mk/main.go"}, nil
	}
	config.sourceFiles = func() ([]string, error) { return []string{"cmd/go-mk/main.go"}, nil }
	_, _, output := runCIChangedCapture(t, config)
	if !strings.Contains(output, "changed=true") {
		t.Fatalf("output = %q, want changed=true", output)
	}
}

func TestRunCIChangedSubmoduleBumpRuns(t *testing.T) {
	fixture := ciGitFixtureNew(t)
	before := ciGitFixtureSubmodule(t, fixture)
	ciGitFixtureWrite(t, fixture.root, "third_party/dependency/README.md", "Updated dependency.\n")
	ciGitFixtureCommit(t, filepath.Join(fixture.root, "third_party", "dependency"))
	ciGitFixtureCommit(t, fixture.root)
	config := ciGitFixtureConfig(fixture)
	config.base = before
	ciGitFixtureAssert(t, config, true, "submodule changed: third_party/dependency")
}

func TestWorkspaceTriggerDirsDropsDotAndAppliesPrefix(t *testing.T) {
	dirs := workspaceTriggerDirs(". third_party/gksyntax", "dots/")
	if len(dirs) != 1 || dirs[0] != "dots/third_party/gksyntax" {
		t.Fatalf("dirs = %v, want [dots/third_party/gksyntax]", dirs)
	}
}

func TestGenerateInputDirsDropsDotAndNormalizes(t *testing.T) {
	dirs := generateInputDirs("./shim . api//v1")
	if len(dirs) != 2 || dirs[0] != "shim" || dirs[1] != "api/v1" {
		t.Fatalf("dirs = %v, want [shim api/v1]", dirs)
	}
}

type ciGitFixture struct {
	root   string
	base   string
	binary string
}

// IsolatedTestEnvironment is exported for the command fixtures in main_test.
func IsolatedTestEnvironment(root string, overrides map[string]string) []string {
	values := map[string]string{
		"PATH":                os.Getenv("PATH"),
		"HOME":                root,
		"XDG_CONFIG_HOME":     filepath.Join(root, "config"),
		"XDG_CACHE_HOME":      filepath.Join(root, "cache"),
		"GIT_CONFIG_GLOBAL":   filepath.Join(root, "gitconfig"),
		"GIT_CONFIG_SYSTEM":   os.DevNull,
		"GIT_AUTHOR_NAME":     "CI fixture",
		"GIT_AUTHOR_EMAIL":    "ci-fixture@example.invalid",
		"GIT_COMMITTER_NAME":  "CI fixture",
		"GIT_COMMITTER_EMAIL": "ci-fixture@example.invalid",
		"GOPATH":              filepath.Join(root, "go-path"),
		"GOCACHE":             filepath.Join(root, "go-build"),
		"GOMODCACHE":          filepath.Join(root, "go-mod"),
		"GOENV":               "off",
		"GOFLAGS":             "-modcacherw",
		"GOTOOLCHAIN":         "local",
		"CGO_ENABLED":         "0",
	}
	for key, value := range overrides {
		values[key] = value
	}
	environment := make([]string, 0, len(values))
	for key, value := range values {
		environment = append(environment, key+"="+value)
	}
	return environment
}

func ciGitFixtureNew(t *testing.T) ciGitFixture {
	t.Helper()
	binary := builtTestEngine(t)
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	globalConfig := filepath.Join(directory, "gitconfig")
	if err := os.WriteFile(globalConfig, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	environment := IsolatedTestEnvironment(directory, map[string]string{
		"GIT_CONFIG_COUNT":    "0",
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_ALLOW_PROTOCOL":  "file",
		"GOCACHE":             filepath.Join(directory, "go-cache"),
		"GOMODCACHE":          filepath.Join(directory, "go-mod-cache"),
		"GOFLAGS":             "",
		"GOWORK":              "off",
		"GOPROXY":             "off",
		"GO111MODULE":         "on",
		"TEST_TELEMETRY_DIR":  filepath.Join(directory, "telemetry"),
	})
	for _, entry := range environment {
		key, value, _ := strings.Cut(entry, "=")
		// This fixture inherits PATH and CGO_ENABLED from the test process.
		if key == "PATH" || key == "CGO_ENABLED" {
			continue
		}
		t.Setenv(key, value)
	}
	// Go telemetry children can write after go list exits.
	ciGitFixtureWrite(t, directory, "telemetry/mode", "off\n")
	root := filepath.Join(directory, "repo")
	ciGitFixtureGit(t, directory, "init", "--initial-branch=main", root)
	ciGitFixtureGit(t, root, "config", "commit.gpgsign", "false")
	ciGitFixtureWrite(t, root, "go.mod", "module example.invalid/ci-fixture\n\ngo 1.20\n")
	ciGitFixtureWrite(t, root, "fixture.go", "package fixture\n\nimport _ \"embed\"\n\n//go:embed assets/input.txt\nvar Input string\n")
	ciGitFixtureWrite(t, root, "assets/input.txt", "initial input\n")
	ciGitFixtureWrite(t, root, "docs/guide.md", "Initial documentation.\n")
	base := ciGitFixtureCommit(t, root)
	origin := filepath.Join(directory, "origin.git")
	ciGitFixtureGit(t, directory, "init", "--bare", "--initial-branch=main", origin)
	ciGitFixtureGit(t, root, "remote", "add", "origin", origin)
	ciGitFixtureGit(t, root, "push", "origin", "main")
	ciGitFixtureGit(t, root, "fetch", "origin")
	t.Chdir(root)
	return ciGitFixture{root: root, base: base, binary: binary}
}

func ciGitFixtureGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func ciGitFixtureWrite(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ciGitFixtureCommit(t *testing.T, root string) string {
	t.Helper()
	ciGitFixtureGit(t, root, "add", "--all")
	ciGitFixtureGit(t, root, "-c", "commit.gpgsign=false", "commit", "-m", "Update fixture files")
	return ciGitFixtureGit(t, root, "rev-parse", "HEAD")
}

type ciGitFixtureOptions struct {
	root           string
	binary         string
	eventName      string
	base           string
	baseIsExact    bool
	head           string
	defaultBranch  string
	refName        string
	workspaceUse   string
	generate       string
	generateInputs string
}

func ciGitFixtureConfig(fixture ciGitFixture) ciGitFixtureOptions {
	return ciGitFixtureOptions{
		root:          fixture.root,
		binary:        fixture.binary,
		eventName:     "push",
		base:          fixture.base,
		head:          "HEAD",
		defaultBranch: "main",
		refName:       "main",
	}
}

func ciGitFixtureAssert(t *testing.T, config ciGitFixtureOptions, changed bool, reason string) {
	t.Helper()
	outputFile := filepath.Join(t.TempDir(), "github_output.txt")
	for key, value := range map[string]string{
		"GO_MK_EVENT_NAME":         config.eventName,
		"GO_MK_DIFF_BASE":          config.base,
		"GO_MK_DIFF_BASE_IS_EXACT": strconv.FormatBool(config.baseIsExact),
		"GO_MK_DIFF_HEAD":          config.head,
		"GO_MK_DEFAULT_BRANCH":     config.defaultBranch,
		"GO_MK_REF_NAME":           config.refName,
		"GO_MK_WORKSPACE_USE":      config.workspaceUse,
		"GO_MK_GENERATE":           config.generate,
		"GO_MK_GENERATE_INPUTS":    config.generateInputs,
		"GITHUB_OUTPUT":            outputFile,
	} {
		t.Setenv(key, value)
	}
	command := exec.Command(config.binary, "ci-changed")
	command.Dir = config.root
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("ci-changed: %v\n%s", err, stderr.String())
	}
	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	output := string(data)
	wantOutput := "changed=false\n"
	if changed {
		wantOutput = "changed=true\n"
	}
	if output != wantOutput || !strings.Contains(stdout.String(), reason) {
		t.Fatalf("output=%q stdout=%q, want output=%q reason=%q\nstderr=%s",
			output, stdout.String(), wantOutput, reason, stderr.String())
	}
}

func ciGitFixtureDiverge(t *testing.T, fixture ciGitFixture) string {
	t.Helper()
	ciGitFixtureGit(t, fixture.root, "checkout", "-b", "side")
	ciGitFixtureWrite(t, fixture.root, "docs/guide.md", "Side documentation.\n")
	side := ciGitFixtureCommit(t, fixture.root)
	ciGitFixtureGit(t, fixture.root, "checkout", "main")
	ciGitFixtureWrite(t, fixture.root, "docs/guide.md", "Main documentation.\n")
	ciGitFixtureCommit(t, fixture.root)
	return side
}

func ciGitFixtureSubmodule(t *testing.T, fixture ciGitFixture) string {
	t.Helper()
	path := filepath.Join(fixture.root, "third_party", "dependency")
	ciGitFixtureGit(t, fixture.root, "init", "--initial-branch=main", path)
	ciGitFixtureWrite(t, path, "README.md", "Initial dependency.\n")
	ciGitFixtureCommit(t, path)
	ciGitFixtureGit(t, fixture.root, "-c", "protocol.file.allow=always", "submodule", "add", path, "third_party/dependency")
	return ciGitFixtureCommit(t, fixture.root)
}
