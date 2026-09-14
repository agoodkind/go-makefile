package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReleaseExportsCgoOptionalFromConsumerMakefile proves a GO_MK_CGO_OPTIONAL
// assignment in a consumer Makefile reaches the environment of the recipes that
// run the go-mk engine. The engine reads the allowlist with os.Getenv, and a
// plain `VAR := value` in a Makefile is a make variable, not an environment
// variable, so without an export in the release module the allowlist silently
// never reaches the cgo-stub check and a release the consumer allowlisted still
// fails.
func TestReleaseExportsCgoOptionalFromConsumerMakefile(t *testing.T) {
	const allowlisted = "github.com/google/certificate-transparency-go/x509"

	files := engineAssets(t)
	releaseModule, err := os.ReadFile(filepath.Join(repoRootForTest(t), "go-release.mk"))
	if err != nil {
		t.Fatalf("read go-release.mk: %v", err)
	}
	files["go-release.mk"] = string(releaseModule)
	server := newFetchServer(t, files)
	dir := newConsumer(t)
	writeMakefile(t, dir, `BINARY := probe
CMD := ./cmd/probe
GO_MK_MODULES := go-release.mk
GO_MK_CGO_OPTIONAL := `+allowlisted+`
include bootstrap.mk
probe-cgo-optional:
	@printf 'cgo_optional=%s\n' "$$GO_MK_CGO_OPTIONAL"
`)

	output, code := runMakeTarget(t, "make", dir, "probe-cgo-optional", map[string]string{
		"GO_MK_CODELOAD_BASE": server.CodeloadBase(),
		"GO_MK_BIN":           filepath.Join(dir, ".make", "go-mk"),
		"HOME":                t.TempDir(),
	})
	if code != 0 {
		t.Fatalf("make probe-cgo-optional exit = %d, want 0: %s", code, output)
	}
	if !strings.Contains(output, "cgo_optional="+allowlisted+"\n") {
		t.Fatalf("recipe environment lacks GO_MK_CGO_OPTIONAL=%s, so the engine never sees the allowlist:\n%s", allowlisted, output)
	}
}
