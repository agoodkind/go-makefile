package main_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	gomk "goodkind.io/go-makefile/cmd/go-mk"
)

func TestStaticcheckReleaseFailurePreservesInstalledAnalyzer(t *testing.T) {
	environment := noticeGitEnvironment(t)
	engine := gomk.BuiltTestEngine(t)
	directory := t.TempDir()
	installed := filepath.Join(directory, ".make", "staticcheck-extra")
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}

	build := exec.Command("go", "build", "-o", installed, "./cmd/staticcheck-extra")
	build.Dir = filepath.Join("..", "..", "staticcheck")
	build.Env = append(environment, "GOPROXY=https://proxy.golang.org", "GOSUMDB=sum.golang.org")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build installed analyzer: %v\n%s", err, output)
	}

	before, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.NotFound(writer, request)
	}))
	t.Cleanup(server.Close)
	command := exec.Command(engine, "staticcheck-extra-bin")
	command.Dir = directory
	command.Env = append(
		environment,
		"_GO_MK_ROOT="+directory,
		"STATICCHECK_EXTRA_BIN=",
		"STATICCHECK_EXTRA_BUILD_REPO=",
		"STATICCHECK_EXTRA_INSTALL=",
		"STATICCHECK_EXTRA_FLAGS=-doc_structure",
		"STATICCHECK_EXTRA_RELEASE_BASE_URL="+server.URL,
	)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "404") {
		t.Fatalf("release lookup did not report the missing release: %v\n%s", err, output)
	}

	if requests.Load() == 0 {
		t.Fatalf("the release command did not contact the release server:\n%s", output)
	}

	after, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(before, after) {
		t.Fatal("a failed release lookup replaced the installed analyzer")
	}
}
