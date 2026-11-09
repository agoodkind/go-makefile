package format

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

var errFormatterFailed = errors.New("source formatter failed")

// Verification records exact comparisons with the complete formatter result.
type Verification struct {
	Normal  map[string]bool
	Changed []string
}

// Verify compares source with configured formatters followed by multilineargs.
//
// The configured formatter reads original files with --diff. Patch application
// and multiline formatting modify temporary copies; original bytes must remain
// equal to the captured input before a file can pass.
//
// A gate supplies its existing tool operations:
//
//	result, err := Verify(command, output, resolve, selectAnalyzer, capture)
func Verify(
	command Command,
	output string,
	resolve func() error,
	selectAnalyzer func() (string, error),
	capture func(string, []string, string) (int, error),
) (Verification, error) {
	slog.Info("verify complete source formatting", slog.Int("files", len(command.Files)))
	source, err := readSelectedFiles(command.Files)
	if err != nil {
		return Verification{}, err
	}

	if err := resolve(); err != nil {
		return Verification{}, err
	}

	analyzer, err := selectAnalyzer()
	if err != nil {
		return Verification{}, err
	}

	if analyzer == "" {
		return Verification{}, errMissingAnalyzer
	}

	args := append([]string{command.Args[0], "--diff"}, command.Args[1:]...)
	status, err := capture(command.Binary, args, output+".configured")
	if err != nil {
		return Verification{}, err
	}

	raw, err := os.ReadFile(output + ".configured")
	if err != nil {
		return Verification{}, failure("read configured formatter output", err)
	}

	if status != 0 && status != 1 || status == 1 && len(raw) == 0 {
		return Verification{}, formatterFailure(status, raw)
	}

	configured, err := applyFormatterPatch(string(raw), source)
	if err != nil {
		return Verification{}, err
	}

	return verifyMultiline(command, source, configured, analyzer, output, capture)
}

func readSelectedFiles(files []string) (map[string][]byte, error) {
	slog.Info("read source for formatting comparison", slog.Int("files", len(files)))
	source := make(map[string][]byte, len(files))
	root, err := os.Getwd()
	if err != nil {
		return nil, failure("resolve formatting directory", err)
	}

	for _, file := range files {
		absolute, err := filepath.Abs(file)
		if err != nil {
			return nil, failure("resolve formatting source", err)
		}

		relative, err := filepath.Rel(root, absolute)
		if err != nil {
			return nil, failure("resolve local formatting source", err)
		}

		if !filepath.IsLocal(relative) {
			return nil, failure(
				"select formatting source",
				fmt.Errorf("%w: %s", errForeignSource, file),
			)
		}

		contents, err := os.ReadFile(absolute)
		if err != nil {
			return nil, failure("read formatting source", err)
		}

		source[absolute] = contents
	}

	return source, nil
}

func verifyMultiline(
	command Command,
	source map[string][]byte,
	configured map[string][]byte,
	analyzer string,
	output string,
	capture func(string, []string, string) (int, error),
) (Verification, error) {
	slog.Info("verify multiline formatting copies", slog.Int("files", len(command.Files)))
	directory, err := os.MkdirTemp("", "go-mk-format-check-")
	if err != nil {
		return Verification{}, failure("create formatting directory", err)
	}

	defer func() { _ = os.RemoveAll(directory) }()

	copies, args, err := writeFormattingCopies(directory, command.Files, configured)
	if err != nil {
		return Verification{}, err
	}

	status, err := capture(analyzer, args, output+".multilineargs")
	if err != nil {
		return Verification{}, err
	}

	if status != 0 {
		raw, err := os.ReadFile(output + ".multilineargs")
		if err != nil {
			return Verification{}, failure("read multiline formatter output", err)
		}

		for original, copyPath := range copies {
			raw = bytes.ReplaceAll(raw, []byte(copyPath), []byte(original))
		}

		return Verification{}, formatterFailure(status, raw)
	}

	return compareCopies(command.Files, source, copies, directory, output)
}

func writeFormattingCopies(
	directory string,
	files []string,
	configured map[string][]byte,
) (map[string]string, []string, error) {
	slog.Info("write temporary formatting copies", slog.Int("files", len(files)))
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, nil, failure("open formatting directory", err)
	}

	defer func() { _ = root.Close() }()

	copies := make(map[string]string, len(files))
	args := []string{"--format-multilineargs"}

	for index, file := range files {
		absolute, err := filepath.Abs(file)
		if err != nil {
			return nil, nil, failure("resolve copied formatting source", err)
		}

		name := fmt.Sprintf("%d.go", index)
		if err := root.WriteFile(name, configured[absolute], 0o600); err != nil {
			return nil, nil, failure("write formatting copy", err)
		}

		copyPath := filepath.Join(directory, name)
		copies[absolute] = copyPath
		args = append(args, copyPath)
	}

	return copies, args, nil
}

func compareCopies(
	files []string,
	source map[string][]byte,
	copies map[string]string,
	directory string,
	output string,
) (Verification, error) {
	slog.Info("compare complete formatting result", slog.Int("files", len(files)))
	root, err := os.OpenRoot(directory)
	if err != nil {
		return Verification{}, failure("open formatting comparison directory", err)
	}

	defer func() { _ = root.Close() }()

	result := Verification{Normal: make(map[string]bool), Changed: nil}
	var text strings.Builder

	for _, file := range files {
		absolute, err := filepath.Abs(file)
		if err != nil {
			return Verification{}, failure("resolve comparison source", err)
		}

		current, err := os.ReadFile(absolute)
		if err != nil {
			return Verification{}, failure("read comparison source", err)
		}

		if !bytes.Equal(current, source[absolute]) {
			return Verification{}, failure(
				"compare formatting source",
				fmt.Errorf("%w: source changed while checking %s", errFormatterFailed, file),
			)
		}

		formatted, err := root.ReadFile(filepath.Base(copies[absolute]))
		if err != nil {
			return Verification{}, failure("read formatting result", err)
		}

		normal := bytes.Equal(current, formatted)
		result.Normal[absolute] = normal
		if !normal {
			result.Changed = append(result.Changed, file)
			fmt.Fprintf(
				&text,
				"%s: source formatting differs after configured formatters and multilineargs\n",
				file,
			)
		}
	}

	if err := os.WriteFile(output, []byte(text.String()), 0o600); err != nil {
		return Verification{}, failure("write formatting comparison", err)
	}

	return result, nil
}

func formatterFailure(status int, raw []byte) error {
	return failure(
		"run source formatter",
		fmt.Errorf(
			"%w (exit status %d): %s",
			errFormatterFailed,
			status,
			strings.TrimSpace(string(raw)),
		),
	)
}
