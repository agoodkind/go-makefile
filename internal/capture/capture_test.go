package capture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConcurrency(t *testing.T) {
	// Expected values are derived from the awk BEGIN block in
	// go_mk_resolve_lint_concurrency: value = int(cpu - load - 1), floored to
	// (cpu < 2 ? 1 : 2) and capped at cpu.
	cases := []struct {
		name        string
		cpuCount    int
		loadAverage float64
		want        int
	}{
		{name: "idle eight cores", cpuCount: 8, loadAverage: 0.0, want: 7},
		{name: "moderate fractional load", cpuCount: 8, loadAverage: 2.9, want: 4},
		{name: "high load floors to minimum", cpuCount: 8, loadAverage: 7.5, want: 2},
		{name: "overloaded clamps to minimum", cpuCount: 8, loadAverage: 20.0, want: 2},
		{name: "single core idle floor one", cpuCount: 1, loadAverage: 0.0, want: 1},
		{name: "single core loaded floor one", cpuCount: 1, loadAverage: 5.0, want: 1},
		{name: "quad core unit load", cpuCount: 4, loadAverage: 1.0, want: 2},
		{name: "sixteen cores fractional", cpuCount: 16, loadAverage: 3.7, want: 11},
		{name: "dual core idle floor two", cpuCount: 2, loadAverage: 0.0, want: 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveConcurrency(tc.cpuCount, tc.loadAverage)
			if got != tc.want {
				t.Fatalf("ResolveConcurrency(%d, %v) = %d, want %d",
					tc.cpuCount, tc.loadAverage, got, tc.want)
			}
		})
	}
}

func TestLintGOFLAGS(t *testing.T) {
	cases := []struct {
		name        string
		existing    string
		concurrency int
		want        string
	}{
		{name: "empty existing", existing: "", concurrency: 4, want: "-p=4"},
		{name: "only existing -p replaced", existing: "-p=2", concurrency: 4, want: "-p=4"},
		{
			name:        "drop -p keep other flags",
			existing:    "-mod=vendor -p=2 -tags=foo",
			concurrency: 4,
			want:        "-mod=vendor -tags=foo -p=4",
		},
		{
			name:        "no -p present appends",
			existing:    "-mod=vendor",
			concurrency: 3,
			want:        "-mod=vendor -p=3",
		},
		{
			name:        "extra whitespace collapses",
			existing:    "  -mod=vendor   -p=8  ",
			concurrency: 5,
			want:        "-mod=vendor -p=5",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LintGOFLAGS(tc.existing, tc.concurrency)
			if got != tc.want {
				t.Fatalf("LintGOFLAGS(%q, %d) = %q, want %q",
					tc.existing, tc.concurrency, got, tc.want)
			}
		})
	}
}

func TestRunCapturesCombinedOutputAndStatus(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "capture.out")

	result, err := Run(
		"/bin/sh",
		[]string{"-c", "echo hello; echo oops >&2"},
		os.Environ(),
		outputPath,
	)
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	if result.Status != 0 {
		t.Fatalf("Run status = %d, want 0", result.Status)
	}
	if result.OutputPath != outputPath {
		t.Fatalf("Run output path = %q, want %q", result.OutputPath, outputPath)
	}

	contents, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatalf("reading capture file: %v", readErr)
	}
	got := string(contents)
	if got != "hello\noops\n" {
		t.Fatalf("capture file contents = %q, want %q", got, "hello\noops\n")
	}
}

func TestRunRecordsNonZeroStatus(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "fail.out")

	result, err := Run("/bin/sh", []string{"-c", "exit 7"}, os.Environ(), outputPath)
	if err != nil {
		t.Fatalf("Run returned unexpected error for non-zero exit: %v", err)
	}
	if result.Status != 7 {
		t.Fatalf("Run status = %d, want 7", result.Status)
	}
}

func TestRunErrorsOnMissingExecutable(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "missing.out")

	_, err := Run("this-binary-does-not-exist-xyz", nil, os.Environ(), outputPath)
	if err == nil {
		t.Fatalf("Run expected an error for a missing executable, got nil")
	}
}
