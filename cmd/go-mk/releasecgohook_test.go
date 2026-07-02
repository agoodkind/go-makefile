package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoMkCgoDepsCgoHookResolvesCompilerEnvironment(t *testing.T) {
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make not available")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}

	testCases := []struct {
		name    string
		env     []string
		wantCC  string
		wantCXX string
	}{
		{
			name: "go_mk_compilers_set_cc_and_cxx",
			env: []string{
				"GO_MK_CC=fake-cross-cc",
				"GO_MK_CXX=fake-cross-cxx",
			},
			wantCC:  "fake-cross-cc",
			wantCXX: "fake-cross-cxx",
		},
		{
			name: "cc_passes_through_without_go_mk_compiler",
			env: []string{
				"CC=ccache gcc",
			},
			wantCC: "ccache gcc",
		},
		{
			name:   "cc_stays_empty_without_compilers",
			wantCC: "",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			workDir := writeCgoHookCompilerFixture(t, repoRoot)
			cmd := exec.Command(makeBin, "go-mk-cgo-deps", "GO_MK_SKIP_FETCH=1")
			cmd.Dir = workDir
			cmd.Env = cgoHookCompilerEnv(workDir, testCase.env)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("make go-mk-cgo-deps failed: %v\n%s", err, output)
			}

			gotCC := readCgoHookObservedCompiler(t, filepath.Join(workDir, "cc-observed.txt"))
			if gotCC != testCase.wantCC {
				t.Fatalf("CC = %q, want %q", gotCC, testCase.wantCC)
			}
			gotCXX := readCgoHookObservedCompiler(t, filepath.Join(workDir, "cxx-observed.txt"))
			if gotCXX != testCase.wantCXX {
				t.Fatalf("CXX = %q, want %q", gotCXX, testCase.wantCXX)
			}
		})
	}
}

func writeCgoHookCompilerFixture(t *testing.T, repoRoot string) string {
	t.Helper()

	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, ".make"), 0o755); err != nil {
		t.Fatalf("mkdir .make: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, ".make", "golangci.yml"), nil, 0o644); err != nil {
		t.Fatalf("seed golangci.yml: %v", err)
	}

	ccObservedPath := filepath.Join(workDir, "cc-observed.txt")
	cxxObservedPath := filepath.Join(workDir, "cxx-observed.txt")
	makefile := fmt.Sprintf(`GO_MK_DEV_DIR := %s
GO_MK_SKIP_FETCH := 1
GO_MK_CGO_DEPS := demolib
include %s

go-mk-cgo-dep-demolib:
	@printf '%%s\n' "$$CC" > "%s"
	@printf '%%s\n' "$$CXX" > "%s"
`, repoRoot, filepath.Join(repoRoot, "go.mk"), ccObservedPath, cxxObservedPath)
	if err := os.WriteFile(filepath.Join(workDir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}

	return workDir
}

func cgoHookCompilerEnv(workDir string, compilerEnv []string) []string {
	env := []string{
		"HOME=" + workDir,
		"GO_MK_SKIP_FETCH=1",
		"GO_MK_TARGET_GOOS=darwin",
		"GO_MK_TARGET_GOARCH=arm64",
		"PATH=" + os.Getenv("PATH"),
	}
	return append(env, compilerEnv...)
}

func readCgoHookObservedCompiler(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return strings.TrimSuffix(string(content), "\n")
}
