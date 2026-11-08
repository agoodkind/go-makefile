package notice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

type listedPackage struct {
	Dir          string         `json:"Dir"`
	ImportPath   string         `json:"ImportPath"`
	GoFiles      []string       `json:"GoFiles"`
	CgoFiles     []string       `json:"CgoFiles"`
	TestGoFiles  []string       `json:"TestGoFiles"`
	XTestGoFiles []string       `json:"XTestGoFiles"`
	Error        *listedError   `json:"Error"`
	DepsErrors   []*listedError `json:"DepsErrors"`
}

type listedError struct {
	Err string `json:"Err"`
}

// SourceFiles maps source files for a platform to their package directories.
func SourceFiles(
	goos string,
	goarch string,
	environment func() []string,
	root func() string,
) (map[string]string, error) {
	slog.Info("notice list source files", slog.String("platform", goos+"/"+goarch))
	command := exec.CommandContext(context.Background(), "go", "list", "-e", "-json", "./...")
	command.Env = sourceEnvironment(environment(), goos, goarch)
	var output, diagnostics bytes.Buffer
	command.Stdout = &output
	command.Stderr = &diagnostics
	if err := command.Run(); err != nil {
		return nil, failure("list notice source for "+goos+"/"+goarch+": "+strings.TrimSpace(diagnostics.String()), err)
	}

	resolvedRoot, err := filepath.EvalSymlinks(root())
	if err != nil {
		return nil, failure("resolve notice source root", err)
	}

	return decodeSourceFiles(&output, resolvedRoot)
}

func sourceEnvironment(environment []string, goos, goarch string) []string {
	environment = replaceEnvironment(environment, "GOOS", goos)
	environment = replaceEnvironment(environment, "GOARCH", goarch)
	if (goos != runtime.GOOS || goarch != runtime.GOARCH) && strings.TrimSpace(os.Getenv("GO_MK_CC")) == "" {
		environment = replaceEnvironment(environment, "CGO_ENABLED", "0")
	}

	return environment
}

func replaceEnvironment(environment []string, name, value string) []string {
	prefix := name + "="

	for index, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			environment[index] = prefix + value
			return environment
		}
	}

	return append(environment, prefix+value)
}

func decodeSourceFiles(reader io.Reader, root string) (map[string]string, error) {
	files := make(map[string]string)
	decoder := json.NewDecoder(reader)

	for {
		var listed listedPackage
		if err := decoder.Decode(&listed); err != nil {
			if errors.Is(err, io.EOF) {
				return files, nil
			}

			return nil, failure("decode notice source listing", err)
		}

		if err := packageLoadError(listed); err != nil {
			return nil, err
		}

		for _, name := range slices.Concat(listed.GoFiles, listed.CgoFiles, listed.TestGoFiles, listed.XTestGoFiles) {
			resolved, err := filepath.EvalSymlinks(filepath.Join(listed.Dir, name))
			if err != nil {
				return nil, failure("resolve notice source "+filepath.Join(listed.Dir, name), err)
			}

			relative, err := filepath.Rel(root, resolved)
			if err != nil {
				return nil, failure("resolve notice source relative to "+root, err)
			}

			file := filepath.ToSlash(relative)
			files[file] = path.Dir(file)
		}
	}
}

func packageLoadError(listed listedPackage) error {
	problems := listed.DepsErrors
	if listed.Error != nil {
		problems = append(problems, listed.Error)
	}

	for _, problem := range problems {
		// Build constraints can omit a package entirely on another platform.
		if strings.HasPrefix(problem.Err, "build constraints exclude all Go files in ") {
			continue
		}

		return failure("load notice source package "+listed.ImportPath, errors.New(problem.Err))
	}

	return nil
}
