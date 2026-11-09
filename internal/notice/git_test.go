package notice_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"goodkind.io/go-makefile/internal/notice"
)

func TestAdoptionPropagatesCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	adopted, found, err := notice.Adoption(ctx, t.TempDir())
	if !errors.Is(err, context.Canceled) || found || !adopted.IsZero() {
		t.Fatalf("canceled adoption = (%s, %t, %v)", adopted, found, err)
	}
}

func TestHistoricalCheckoutInitializesRecordedSubmodule(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.invalid")
	root := t.TempDir()
	dependency := filepath.Join(root, "dependency")

	consumer := filepath.Join(root, "consumer")
	for _, directory := range []string{dependency, consumer} {
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}

		git(t, directory, "init", "-q")
	}

	input := filepath.Join(dependency, "source.txt")
	if err := os.WriteFile(input, []byte("pinned"), 0o600); err != nil {
		t.Fatal(err)
	}

	git(t, dependency, "add", ".")
	git(t, dependency, "commit", "-qm", "Record dependency")
	git(t, consumer, "submodule", "add", dependency, "third_party/dependency")
	git(
		t,
		consumer,
		"config",
		"-f",
		".gitmodules",
		"submodule.third_party/dependency.update",
		"none",
	)
	git(t, consumer, "commit", "-qam", "Pin dependency")
	if err := os.WriteFile(input, []byte("newer"), 0o600); err != nil {
		t.Fatal(err)
	}

	git(t, dependency, "commit", "-qam", "Advance dependency")
	checkout := filepath.Join(root, "history")
	git(t, root, "clone", "--no-checkout", consumer, checkout)
	if err := notice.HistoryGit(checkout, []string{"checkout", "--detach", "HEAD"}); err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(filepath.Join(checkout, "third_party/dependency/source.txt"))
	if err != nil || string(contents) != "pinned" {
		t.Fatalf("historical dependency = %q, error = %v", contents, err)
	}

	if status := git(t, consumer, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("historical preparation modified its source repository: %s", status)
	}
}

func git(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}

	return string(output)
}
