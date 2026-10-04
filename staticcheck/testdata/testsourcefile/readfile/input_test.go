package readfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/testsourcefile/readfile"
)

const sharedDocument = "../../instances/network.json"

func TestPassesContentToProduction(t *testing.T) {
	raw, err := os.ReadFile(sharedDocument)
	if err != nil {
		t.Fatal(err)
	}
	if readfile.Parse(raw) == 0 {
		t.Fatal("Parse returned 0")
	}
}

func TestWritesMutatedCopyForProduction(t *testing.T) {
	raw, err := os.ReadFile(sharedDocument)
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(raw), "a", "b", 1)
	path := filepath.Join(t.TempDir(), "network.json")
	if err := os.WriteFile(path, []byte(mutated), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readfile.Load(path); err != nil {
		t.Fatal(err)
	}
}

func TestOpenedFilePassedThroughParent(t *testing.T) {
	root := t.Name()
	raw, err := os.ReadFile(filepath.Join(root, "..", "network.json"))
	if err != nil {
		t.Fatal(err)
	}
	if readfile.Parse(raw) == 0 {
		t.Fatal("Parse returned 0")
	}
}

func TestAssertsOnText(t *testing.T) {
	raw, err := os.ReadFile(sharedDocument) // want `reads the file at the constant path "../../instances/network.json"`
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "table-id") {
		t.Fatal("document omits table-id")
	}
	if readfile.Parse(nil) != 0 {
		t.Fatal("Parse(nil) != 0")
	}
}
