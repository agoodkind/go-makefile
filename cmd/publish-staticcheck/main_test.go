package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishStaticcheck(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "publish-staticcheck")
	runCommand(t, ".", "go", "build", "-o", binary, ".")
	remote := filepath.Join(root, "remote.git")
	runCommand(t, root, "git", "init", "--bare", remote)
	repository := filepath.Join(root, "repository")
	runCommand(t, root, "git", "init", "--initial-branch=main", repository)
	runCommand(t, repository, "git", "remote", "add", "origin", remote)
	if err := os.Mkdir(filepath.Join(repository, "staticcheck"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repository, "staticcheck/go.mod", "module example.com/analyzer\n\ngo 1.26\n")
	writeFile(t, repository, "staticcheck/value.go", "package analyzer\n\nconst Value = 1\n")
	initial := commit(t, repository)
	runCommand(t, repository, "git", "tag", "staticcheck/v0.2.0", initial)
	runCommand(t, repository, "git", "push", "origin", "main", "refs/tags/staticcheck/v0.2.0")

	assertVersion(t, runCommand(t, repository, binary, initial), "v0.2.0")
	writeFile(t, repository, "staticcheck/value.go", "package analyzer\n\nconst Value = 2\n")
	changed := commit(t, repository)
	runCommand(t, repository, "git", "push", "origin", "main")
	assertVersion(t, runCommand(t, repository, binary, changed), "v0.2.1")
	assertTag(t, remote, "staticcheck/v0.2.1", changed)
	assertVersion(t, runCommand(t, repository, binary, changed), "v0.2.1")
	assertTagCount(t, remote, 2)

	writeFile(t, repository, "README.md", "Repository documentation.\n")
	documentation := commit(t, repository)
	runCommand(t, repository, "git", "push", "origin", "main")
	assertVersion(t, runCommand(t, repository, binary, documentation), "v0.2.1")
	assertTagCount(t, remote, 2)

	writeFile(t, repository, "staticcheck/value.go", "package analyzer\n\nconst Value = 3\n")
	next := commit(t, repository)
	runCommand(t, repository, "git", "push", "origin", "main")
	assertVersion(t, runCommand(t, repository, binary, next), "v0.2.2")
	assertTag(t, remote, "staticcheck/v0.2.2", next)
	assertVersion(t, runCommand(t, repository, binary, initial), "v0.2.2")
	assertTagCount(t, remote, 3)

	writeFile(t, repository, "staticcheck/value.go", "package analyzer\n\nconst Value = 4\n")
	unpublished := commit(t, repository)
	command := exec.Command(binary, unpublished)
	command.Dir = repository
	command.Env = testEnvironment()
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "is not on origin/main") {
		t.Fatalf("publisher accepted an unmerged commit: %v\n%s", err, output)
	}
	assertTagCount(t, remote, 3)
}

func commit(t *testing.T, directory string) string {
	t.Helper()
	runCommand(t, directory, "git", "add", ".")
	runCommand(t, directory, "git", "commit", "--message", "Update source")
	return runCommand(t, directory, "git", "rev-parse", "HEAD")
}

func writeFile(t *testing.T, directory, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runCommand(t *testing.T, directory, name string, args ...string) string {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = directory
	command.Env = testEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func testEnvironment() []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Release test",
		"GIT_AUTHOR_EMAIL=release@example.invalid",
		"GIT_COMMITTER_NAME=Release test",
		"GIT_COMMITTER_EMAIL=release@example.invalid",
	)
}

func assertVersion(t *testing.T, output, version string) {
	t.Helper()
	if !strings.Contains(output, "version="+version) {
		t.Fatalf("publisher did not select %s:\n%s", version, output)
	}
}

func assertTag(t *testing.T, remote, tag, target string) {
	t.Helper()
	actual := runCommand(t, ".", "git", "--git-dir="+remote, "rev-parse", "refs/tags/"+tag)
	if actual != target {
		t.Fatalf("tag %s points to %s, want %s", tag, actual, target)
	}
}

func assertTagCount(t *testing.T, remote string, count int) {
	t.Helper()
	tags := runCommand(t, ".", "git", "--git-dir="+remote, "tag", "--list")
	if actual := len(strings.Fields(tags)); actual != count {
		t.Fatalf("remote contains %d tags, want %d:\n%s", actual, count, tags)
	}
}
