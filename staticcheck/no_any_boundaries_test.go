package staticcheck_test

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"goodkind.io/go-makefile/staticcheck"
)

func TestNoAnyExternalSignatures(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	if err := os.CopyFS(directory, os.DirFS(filepath.Join("testdata", "no_any"))); err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, directory, staticcheck.NoAnyOrEmptyInterfaceAnalyzer, "consumer")
}
