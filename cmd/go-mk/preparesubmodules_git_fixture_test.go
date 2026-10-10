package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gomk "goodkind.io/go-makefile/cmd/go-mk"
)

type subRepoFixture struct {
	checkout string
	binary   string
	env      []string
}

func TestPrepareSubmodulesForOutputsInterleavesNestedDiscovery(t *testing.T) {
	fixture := subRepoColdCheckout(t)
	parent := filepath.Join(fixture.checkout, "third_party", "gksyntax")
	subRepoAssertAbsent(t, filepath.Join(parent, ".gitmodules"))
	subRepoPrepare(t, fixture, "third_party/gksyntax/treesitter/grammars/swift/upstream/src/parser.c "+
		"third_party/gksyntax/treesitter/grammars/perl/upstream/src/parser.c")
	for _, language := range []string{"swift", "perl"} {
		parser := filepath.Join(parent, "treesitter", "grammars", language, "upstream", "src", "parser.c")
		subRepoAssertFile(t, parser, language+" parser\n")
	}
}

func TestPrepareSubmodulesForOutputsExcludesUnrelatedSubmodule(t *testing.T) {
	fixture := subRepoColdCheckout(t)
	parent := filepath.Join(fixture.checkout, "third_party", "gksyntax")
	subRepoPrepare(t, fixture, "third_party/gksyntax/other/file.go")
	subRepoAssertFile(t, filepath.Join(parent, "other", "file.go"), "package other\n")
	for _, language := range []string{"swift", "perl", "dart"} {
		submodule := filepath.Join(parent, "treesitter", "grammars", language, "upstream")
		subRepoAssertAbsent(t, filepath.Join(submodule, ".git"))
		subRepoAssertAbsent(t, filepath.Join(submodule, "src", "parser.c"))
	}
}

func subRepoPrepare(t *testing.T, fixture subRepoFixture, outputs string) {
	t.Helper()
	command := exec.Command(fixture.binary, "prepare-generated-submodules")
	command.Dir = fixture.checkout
	command.Env = append(fixture.env, "GO_MK_GENERATE_OUTPUTS="+outputs)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("prepare-generated-submodules failed: %v\n%s", err, output)
	}
}

func subRepoColdCheckout(t *testing.T) subRepoFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the Git fixture uses /dev/null")
	}
	fixture := t.TempDir()
	home := filepath.Join(fixture, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	gitConfig := filepath.Join(fixture, "gitconfig")
	subRepoWriteFile(t, gitConfig, "[protocol \"file\"]\n\tallow = always\n"+
		"[commit]\n\tgpgsign = false\n[init]\n\tdefaultBranch = main\n")
	environment := gomk.IsolatedTestEnvironment(fixture, map[string]string{
		"HOME":                home,
		"XDG_CACHE_HOME":      filepath.Join(home, "cache"),
		"XDG_CONFIG_HOME":     filepath.Join(home, "config"),
		"GIT_CONFIG_COUNT":    "0",
		"GIT_ALLOW_PROTOCOL":  "file",
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_AUTHOR_NAME":     "Submodule Test",
		"GIT_AUTHOR_EMAIL":    "submodule-test@example.invalid",
		"GIT_COMMITTER_NAME":  "Submodule Test",
		"GIT_COMMITTER_EMAIL": "submodule-test@example.invalid",
	})
	binary := subRepoBuildEngine(t, fixture, environment)
	// The Go settings apply only to the engine build in this fixture.
	var env []string
	for _, entry := range environment {
		if strings.HasPrefix(entry, "GO") || strings.HasPrefix(entry, "CGO_ENABLED=") {
			continue
		}
		env = append(env, entry)
	}

	parent := subRepoInit(t, env, filepath.Join(fixture, "gksyntax"), "other/file.go", "package other\n")
	for _, language := range []string{"swift", "perl", "dart"} {
		grammar := subRepoInit(t, env, filepath.Join(fixture, language), "src/parser.c", language+" parser\n")
		subRepoGit(t, env, parent, "submodule", "add", grammar, "treesitter/grammars/"+language+"/upstream")
	}
	subRepoGit(t, env, parent, "commit", "-am", "Add grammar submodules")
	consumer := subRepoInit(t, env, filepath.Join(fixture, "consumer"), "README.md", "Consumer fixture\n")
	subRepoGit(t, env, consumer, "submodule", "add", parent, "third_party/gksyntax")
	subRepoGit(t, env, consumer, "commit", "-am", "Add syntax submodule")
	checkout := filepath.Join(fixture, "checkout")
	subRepoGit(t, env, fixture, "clone", "--no-recurse-submodules", consumer, checkout)
	return subRepoFixture{checkout: checkout, binary: binary, env: env}
}

func subRepoBuildEngine(t *testing.T, fixture string, env []string) string {
	t.Helper()
	binary := filepath.Join(fixture, "go-mk")
	command := exec.Command("go", "build", "-o", binary, ".")
	command.Env = append(env,
		"GOPROXY=https://proxy.golang.org",
		"GOSUMDB=sum.golang.org",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build go-mk failed: %v\n%s", err, output)
	}
	return binary
}

func subRepoInit(t *testing.T, env []string, directory, file, content string) string {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	subRepoGit(t, env, directory, "init")
	subRepoWriteFile(t, filepath.Join(directory, filepath.FromSlash(file)), content)
	subRepoGit(t, env, directory, "add", ".")
	subRepoGit(t, env, directory, "commit", "-m", "Add fixture content")
	return directory
}

func subRepoGit(t *testing.T, env []string, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Env = env
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", arguments, directory, err, output)
	}
}

func subRepoWriteFile(t *testing.T, file, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func subRepoAssertFile(t *testing.T, file, expected string) {
	t.Helper()
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read initialized submodule file %s: %v", file, err)
	}
	if string(content) != expected {
		t.Fatalf("%s content = %q, want %q", file, content, expected)
	}
}

func subRepoAssertAbsent(t *testing.T, file string) {
	t.Helper()
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("%s must be absent, stat error = %v", file, err)
	}
}
