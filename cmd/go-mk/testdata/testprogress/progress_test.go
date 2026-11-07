package progress

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

var stampedVersion = "unset"

func TestLive(t *testing.T) {
	t.Run("parallel", func(t *testing.T) {
		t.Parallel()
		t.Log("parallel test completed")
	})
	t.Run("active", func(t *testing.T) {
		_, _ = fmt.Fprintln(os.Stderr, "child stderr diagnostic")
		client := http.Client{Timeout: 70 * time.Second}
		response, err := client.Get(os.Getenv("GO_MK_PROGRESS_GATE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("gate status = %d", response.StatusCode)
		}
	})
}

func TestAfter(t *testing.T) {
	t.Log("stamp:", stampedVersion)
	t.Attr("kind", "progress fixture")
	_, _ = fmt.Fprintln(os.Stdout, "=== RUN   ordinary stdout diagnostic")
	_, _ = fmt.Fprintln(os.Stderr, "--- FAIL: ordinary stderr diagnostic")
	_, _ = fmt.Fprintln(os.Stdout, "PASS")
	_, _ = fmt.Fprintln(os.Stderr, "FAIL")
	if err := os.WriteFile(t.ArtifactDir()+"/result.txt", []byte("test artifact"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSkip(t *testing.T) {
	t.Skip("skip diagnostic")
}

func TestFailure(t *testing.T) {
	t.Fatal("intentional test failure diagnostic")
}

func TestExit(t *testing.T) {
	os.Exit(0)
}

func BenchmarkProgress(benchmark *testing.B) {
	for range benchmark.N {
		benchmark.Helper()
	}
}
