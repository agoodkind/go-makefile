package cigate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQualityGateManifest(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("go.mod", []byte("module example.com/quality\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GOLANGCI_LINT_INSTALL", "example.com/golangci-lint@v2.0.0")
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		gate       string
		lintGates  string
		toolchain  string
		restoreCgo bool
		lintCache  bool
	}{
		{name: "vet uses the compile job cache", gate: "vet", toolchain: "cc-1", restoreCgo: true},
		{name: "format skips the compile job cache", gate: "lint-format", toolchain: "cc-1"},
		{name: "aggregate uses the default lint gates", gate: "build-check", lintCache: true},
		{name: "aggregate omits disabled lint", gate: "build-check", lintGates: "lint-format"},
		{
			name:      "explicit lint runs regardless of aggregate config",
			gate:      "lint-golangci",
			lintGates: "lint-format",
			lintCache: true,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "github-output")
				t.Setenv("GITHUB_OUTPUT", path)
				t.Setenv("GO_MK_CI_QUALITY_GATE", testCase.gate)
				t.Setenv("GO_MK_CGO_TOOLCHAIN_ID", testCase.toolchain)
				t.Setenv("LINT_GATES", testCase.lintGates)
				cacheManifest := func() int {
					err := appendOutputs("lint_cache_paths=/workspace/.make/golangci-cache\n")
					if err != nil {
						t.Fatal(err)
					}

					return 0
				}

				if err := writeGateManifest(
					cacheManifest,
					func() string { return "gateway" },
					"lint-golangci lint-format",
					now,
				); err != nil {
					t.Fatal(err)
				}

				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}

				outputs := make(map[string]string)
				for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
					name, value, _ := strings.Cut(line, "=")
					outputs[name] = value
				}

				if outputs["quality_gate"] != testCase.gate || outputs["quality_gate_command"] != "make "+testCase.gate {
					t.Fatalf("quality gate outputs = %v, want target %s", outputs, testCase.gate)
				}

				if got := outputs["restore_cgo"] == "true"; got != testCase.restoreCgo {
					t.Errorf("restore_cgo = %t, want %t", got, testCase.restoreCgo)
				}

				if got := outputs["lint_cache_paths"]; (got != "") != (testCase.restoreCgo || testCase.lintCache) {
					t.Errorf("existing cache manifest paths = %q", got)
				}

				key, prefix := outputs["lint_cache_key"], outputs["lint_cache_restore_key"]
				if testCase.lintCache && (prefix == "" || !strings.HasPrefix(
					key,
					prefix,
				) || !strings.HasSuffix(key, "-2026-01")) {
					t.Errorf(
						"key %q, prefix %q, want a shared prefix and the UTC week in the primary key",
						key,
						prefix,
					)
				}

				if !testCase.lintCache && (key != "" || prefix != "") {
					t.Errorf("key %q, prefix %q, want empty keys", key, prefix)
				}
			},
		)
	}
}

func TestQualityGateManifestRejectsUnsupportedTarget(t *testing.T) {
	t.Setenv("GO_MK_CI_QUALITY_GATE", "Vet")
	path := filepath.Join(t.TempDir(), "github-output")
	t.Setenv("GITHUB_OUTPUT", path)
	t.Setenv("GO_MK_JOB_LAYOUT", "quality-gate")

	cacheManifest := func() int {
		t.Fatal("unsupported target requested the cache manifest")
		return 1
	}

	now := func() time.Time { return time.Time{} }
	if status := Run(
		cacheManifest,
		func() string { return "." },
		"lint-golangci",
		now,
	); status == 0 {
		t.Fatal("unsupported target succeeded")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unsupported target output file: %v, want no file", err)
	}
}
