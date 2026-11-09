package vet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/build"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Commands selects production files for each Go vet command.
//
// The Go package list supplies the files selected for the current platform.
//
// For example, an empty target setting selects all packages:
//
//	GO_VET_TARGETS=./...
func Commands(
	expand func([]string) ([]string, error),
	environment func() []string,
) ([][]string, error) {
	targets := os.Getenv("GO_VET_TARGETS")
	if targets == "" {
		targets = "./..."
	}

	packages, err := expand(strings.Fields(targets))
	if err != nil {
		return nil, err
	}

	if len(packages) == 0 {
		return nil, nil
	}

	slog.Info("lint list production packages for vet")
	command := exec.Command("go", append([]string{"list", "-json"}, packages...)...)
	command.Env = environment()
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(output))
	commands := make([][]string, 0)
	for decoder.More() {
		var pkg build.Package
		if err := decoder.Decode(&pkg); err != nil {
			wrapped := fmt.Errorf("lint: decode go list output: %w", err)
			slog.Error("lint decode go list output failed", slog.Any("err", wrapped))

			return nil, wrapped
		}

		files := append(pkg.GoFiles, pkg.CgoFiles...)
		if len(files) == 0 {
			continue
		}

		arguments := []string{"vet"}
		for _, file := range files {
			arguments = append(arguments, filepath.Join(pkg.Dir, file))
		}

		commands = append(commands, arguments)
	}

	return commands, nil
}
