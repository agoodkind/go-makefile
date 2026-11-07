package format

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

func whitespaceConfiguration() (string, []string, []string, error) {
	path, flags, err := configurationFlags(strings.Fields(os.Getenv("GOLANGCI_LINT_FLAGS")))
	if err != nil {
		return "", nil, nil, err
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return "", nil, nil, failure("read golangci-lint configuration", err)
	}

	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return "", nil, nil, failure("parse golangci-lint configuration", err)
	}

	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return "", nil, nil, fmt.Errorf("golangci-lint configuration must contain a YAML mapping")
	}

	root := document.Content[0]
	run := mappingValue(root, "run")
	setScalar(run, "tests", "true", "!!bool")
	var tags []string
	if node := existingValue(run, "build-tags"); node != nil {
		if err := node.Decode(&tags); err != nil {
			return "", nil, nil, failure("read formatting build tags", err)
		}
	}

	paths := existingValue(mappingValue(mappingValue(root, "linters"), "exclusions"), "paths")
	if paths != nil {
		selected := paths.Content[:0]
		for _, path := range paths.Content {
			if path.Value != `_test\.go` && path.Value != `_test\.go$` {
				selected = append(selected, path)
			}
		}

		paths.Content = selected
	}

	contents, err = yaml.Marshal(&document)
	if err != nil {
		return "", nil, nil, failure("encode whitespace configuration", err)
	}

	// Use the original directory for config-relative exclusions and build paths.
	temporary, err := temporaryFile(filepath.Dir(path), ".go-mk-whitespace-*.yml", contents)

	return temporary, flags, tags, err
}

func configurationFlags(flags []string) (string, []string, error) {
	var path string
	var remaining []string

	for index := 0; index < len(flags); index++ {
		flag := flags[index]
		switch {
		case flag == "-c" || flag == "--config":
			index++
			if index == len(flags) {
				return "", nil, fmt.Errorf("%s requires a configuration path", flag)
			}

			path = flags[index]
		case strings.HasPrefix(flag, "--config="):
			path = strings.TrimPrefix(flag, "--config=")
		case strings.HasPrefix(flag, "-c="):
			path = strings.TrimPrefix(flag, "-c=")
		default:
			remaining = append(remaining, flag)
		}
	}

	if path != "" {
		return path, remaining, nil
	}

	directory, err := os.Getwd()
	if err != nil {
		return "", nil, failure("resolve configuration directory", err)
	}

	for {
		for _, name := range []string{".golangci.yml", ".golangci.yaml", ".golangci.json"} {
			candidate := filepath.Join(directory, name)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, remaining, nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return "", nil, failure("find golangci-lint configuration", err)
			}
		}

		parent := filepath.Dir(directory)
		if parent == directory {
			return "", nil, fmt.Errorf("make fmt requires a golangci-lint YAML configuration; set GOLANGCI_LINT_FLAGS=-c <path>")
		}

		directory = parent
	}
}

func existingValue(mapping *yaml.Node, key string) *yaml.Node {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}

	return nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if value := existingValue(mapping, key); value != nil {
		return value
	}

	value := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)

	return value
}

func setScalar(mapping *yaml.Node, key, value, tag string) {
	node := existingValue(mapping, key)
	if node == nil {
		node = &yaml.Node{}
		mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, node)
	}

	*node = yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
}
