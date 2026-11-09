package history

import "strings"

const renameFieldCount = 3

func (check comparison) output(message string, arguments ...string) (string, error) {
	output, err := check.git(message, arguments...)
	if err != nil {
		return "", failed(message, err)
	}

	return output, nil
}

func (check comparison) historicalModule(revision, currentPath string) (string, error) {
	output, err := check.output(
		"notice follow module renames",
		"log",
		"--follow",
		"--diff-filter=R",
		"--format=",
		"--name-status",
		"-z",
		revision+"..HEAD",
		"--",
		"go.mod",
	)
	if err != nil {
		return "", err
	}

	fields := strings.Split(strings.Trim(output, "\x00\n"), "\x00")
	for index := 0; index+2 < len(fields); index += renameFieldCount {
		if fields[index+2] == currentPath {
			currentPath = fields[index+1]
		}
	}

	return currentPath, nil
}

func (check comparison) renames(revision string) (map[string]string, error) {
	output, err := check.output(
		"notice history renames",
		"diff",
		"--name-status",
		"-z",
		"--find-renames",
		revision,
		"HEAD",
	)
	if err != nil {
		return nil, err
	}

	fields := strings.Split(strings.TrimSuffix(output, "\x00"), "\x00")
	renames := make(map[string]string)

	for index := 0; index+1 < len(fields); {
		status := fields[index]
		previous := fields[index+1]
		index += 2
		if !strings.HasPrefix(status, "R") {
			continue
		}

		if index >= len(fields) {
			return nil, errIncompleteRename
		}

		renames[previous] = fields[index]
		index++
	}

	return renames, nil
}
