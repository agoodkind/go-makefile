package notice

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// Files lists the source and test files selected for a target platform.
//
// Historical capture compares every selected file with the current platform.
// Cross compilation disables cgo unless the caller supplies GO_MK_CC.
//
// A Linux source listing uses:
//
//	Files(".", "linux", "amd64", os.Environ())
func Files(
	root string,
	goos string,
	goarch string,
	environment []string,
) (map[string]string, error) {
	slog.Info("notice list source files", slog.String("platform", goos+"/"+goarch))
	command := exec.Command(
		"go",
		"list",
		"-e",
		"-f",
		`{{$dir := .Dir}}{{range .GoFiles}}{{$dir}}/{{.}}{{"\n"}}{{end}}{{range .CgoFiles}}{{$dir}}/{{.}}{{"\n"}}{{end}}{{range .TestGoFiles}}{{$dir}}/{{.}}{{"\n"}}{{end}}{{range .XTestGoFiles}}{{$dir}}/{{.}}{{"\n"}}{{end}}`,
		"./...",
	)
	command.Env = append(slices.Clone(environment), "GOOS="+goos, "GOARCH="+goarch)
	if (goos != runtime.GOOS || goarch != runtime.GOARCH) &&
		strings.TrimSpace(os.Getenv("GO_MK_CC")) == "" {
		command.Env = append(command.Env, "CGO_ENABLED=0")
	}

	output, err := command.Output()
	if err != nil {
		return nil, failed("list historical platform source", err)
	}

	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, failed("resolve historical source root", err)
	}

	files := make(map[string]string)

	for _, listed := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if listed == "" {
			continue
		}

		resolved, err := filepath.EvalSymlinks(listed)
		if err != nil {
			return nil, failed("resolve historical source file", err)
		}

		relative, err := filepath.Rel(root, resolved)
		if err != nil {
			return nil, failed("resolve historical source path", err)
		}

		file := filepath.ToSlash(relative)
		files[file] = path.Dir(file)
	}

	return files, nil
}

func failed(operation string, err error) error {
	wrapped := fmt.Errorf("%s: %w", operation, err)
	slog.Error(
		"notice source listing failed",
		slog.String("operation", operation),
		slog.Any("err", wrapped),
	)

	return wrapped
}
