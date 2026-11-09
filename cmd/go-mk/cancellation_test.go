package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"goodkind.io/go-makefile/internal/notice/history"
)

func TestNoticeHistoryCancelsRunningPreparation(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GOLANGCI_LINT", "")
	t.Setenv("STATICCHECK_EXTRA_BIN", "")
	makefile, err := os.ReadFile(filepath.Join("testdata", "adoption", "cancellation.mk"))
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	chdir(t, root)
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for name, contents := range map[string]string{
		"go.mod":   "module example.com/consumer\n\ngo 1.26\n",
		"Makefile": string(makefile),
	} {
		if err := os.WriteFile(name, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, arguments := range [][]string{
		{"init", "--quiet"},
		{"add", "."},
		{
			"-c",
			"user.name=Notice test",
			"-c",
			"user.email=notice@example.invalid",
			"-c",
			"commit.gpgsign=false",
			"commit",
			"--quiet",
			"--message",
			"Record preparation",
		},
	} {
		command := exec.CommandContext(t.Context(), "git", arguments...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", arguments, err, output)
		}
	}

	if err := os.Mkdir(".make", 0o700); err != nil {
		t.Fatal(err)
	}

	findingsPath := filepath.Join(root, "findings.out")
	if err := os.WriteFile(findingsPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(root, "preparation-started")
	t.Setenv("NOTICE_CANCEL_MARKER", marker)
	t.Setenv("GO_MK_NOTICE_MAKEFILE", "")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- history.RestrictFindings(
			ctx,
			findingsPath,
			time.Now().Add(time.Minute),
			"staticcheck-extra",
			"",
			loggedGitOutput,
			captureNoticeStaticcheckFindings,
		)
	}()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(time.Second * 5)
	defer timeout.Stop()

	for {
		select {
		case err := <-result:
			t.Fatalf("historical capture finished before cancellation: %v", err)
		case <-timeout.C:
			cancel()
			<-result
			t.Fatal("historical preparation did not start")
		case <-ticker.C:
			if _, err := os.Stat(marker); err == nil {
				cancel()
				if err := <-result; !errors.Is(err, context.Canceled) {
					t.Fatalf("historical preparation cancellation = %v", err)
				}

				if directory, err := os.Getwd(); err != nil || directory != workingDirectory {
					t.Fatalf("working directory after cancellation = %q, %v", directory, err)
				}

				return
			}
		}
	}
}
