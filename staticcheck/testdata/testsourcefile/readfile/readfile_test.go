package readfile_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

const (
	sourcePath = "readfile.go"
	sourceBase = "read"
)

type reader struct{}

func (reader) ReadFile(name string) ([]byte, error) {
	return nil, nil
}

func ReadFile(name string) ([]byte, error) {
	return nil, nil
}

func TestFlagged(t *testing.T) {
	t.Parallel()

	_, _ = os.ReadFile("readfile.go")                      // want `reads the file at the constant path "readfile.go"`
	_, _ = os.ReadFile(sourcePath)                         // want `reads the file at the constant path "readfile.go"`
	_, _ = os.ReadFile(sourceBase + "file.go")             // want `reads the file at the constant path "readfile.go"`
	_, _ = os.Open(filepath.Join("..", "readfile", "a.go")) // want `reads the file at the constant path "../readfile/a.go"`
	_, _ = os.OpenFile("go.mod", os.O_RDONLY, 0)           // want `reads the file at the constant path "go.mod"`
	_, _ = os.ReadFile("/var/data.txt")                    // want `reads the file at the constant path "/var/data.txt"`
}

func TestSourceThroughParent(t *testing.T) {
	t.Parallel()

	root := t.Name()
	_, _ = os.ReadFile(filepath.Join(root, "..", "readfile.go")) // want `reads a file through a parent directory path`
	_, _ = os.ReadFile(filepath.Join(root, "..", "testdata", "x"))
	_, _ = parser.ParseFile(token.NewFileSet(), root, nil, 0) // want `parses Go source with go/parser`
}

func TestClean(t *testing.T) {
	t.Parallel()

	variablePath := "readfile.go"
	_, _ = os.ReadFile("testdata/input.json")
	_, _ = os.ReadFile("../testdata/x")
	_, _ = os.Open(filepath.Join("testdata", "x.txt"))
	_, _ = os.ReadFile(filepath.Join(t.TempDir(), "x"))
	_, _ = os.ReadFile(variablePath)
	_, _ = os.ReadFile("/dev/null")
	_, _ = os.ReadFile("/proc/self/status")
	_, _ = os.ReadFile("/sys/kernel/x")
	_, _ = os.ReadFile("/etc/hosts")
	_, _ = ReadFile("readfile.go")
	_, _ = reader{}.ReadFile("readfile.go")
}
