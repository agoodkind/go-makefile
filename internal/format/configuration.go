package format

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	configFlag   = "--config"
	noConfigFlag = "--no-config"
)

var (
	errConfigMapping = errors.New("golangci-lint configuration must contain a YAML mapping")
	errConfigPath    = errors.New("requires a configuration path")
	errConfigFlags   = errors.New("--no-config cannot be used with a configuration path")
	errConfigMissing = errors.New("set GOLANGCI_LINT_FLAGS=-c <path> to select a golangci-lint configuration")
)

func whitespaceConfiguration() (string, []string, []string, error) {
	return whitespaceSettings(strings.Fields(os.Getenv("GOLANGCI_LINT_FLAGS")), true)
}

func whitespaceSettings(arguments []string, formatTests bool) (string, []string, []string, error) {
	path, flags, err := configurationFlags(arguments)
	if err != nil {
		return "", nil, nil, err
	}

	contents := []byte("version: \"2\"\n")
	if path != "" {
		contents, err = readConfig(path)
		if err != nil {
			return "", nil, nil, err
		}
	}

	var document yaml.Node
	err = yaml.Unmarshal(contents, &document)
	if err != nil {
		return "", nil, nil, failure("parse golangci-lint configuration", err)
	}

	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return "", nil, nil, errConfigMapping
	}

	root := document.Content[0]
	run := mappingValue(root, "run")
	if formatTests {
		setScalar(run, "tests", "true", "!!bool")
	}

	var tags []string
	if node := existingValue(run, "build-tags"); node != nil {
		err = node.Decode(&tags)
		if err != nil {
			return "", nil, nil, failure("read formatting build tags", err)
		}
	}

	exclusions := mappingValue(mappingValue(root, "linters"), "exclusions")
	paths := existingValue(exclusions, "paths")
	if paths != nil {
		selected := paths.Content[:0]
		for _, path := range paths.Content {
			if path.Anchor == "go_makefile_test_files" {
				continue
			}

			if formatTests && (path.Value == `_test\.go` || path.Value == `_test\.go$`) {
				continue
			}

			selected = append(selected, path)
		}

		paths.Content = selected
	}

	generated := existingValue(exclusions, "generated")
	if generated == nil || generated.Anchor == "go_makefile_generated" {
		setScalar(exclusions, "generated", "disable", "!!str")
	}

	contents, err = yaml.Marshal(&document)
	if err != nil {
		return "", nil, nil, failure("encode whitespace configuration", err)
	}

	// Use the original directory for config-relative exclusions and build paths.
	temporary, err := temporaryFile(filepath.Dir(path), ".go-mk-whitespace-*.yml", contents)

	return temporary, flags, tags, err
}

func readConfig(path string) ([]byte, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, failure("resolve golangci-lint configuration", err)
	}

	directory, err := os.OpenRoot(filepath.Dir(resolved))
	if err != nil {
		return nil, failure("open golangci-lint configuration directory", err)
	}

	defer func() { _ = directory.Close() }()

	contents, err := directory.ReadFile(filepath.Base(resolved))
	if err != nil {
		return nil, failure("read golangci-lint configuration", err)
	}

	return contents, nil
}

func configurationFlags(flags []string) (string, []string, error) {
	var path string
	var remaining []string
	var noConfig bool

	for index := 0; index < len(flags); index++ {
		flag := flags[index]
		switch {
		case flag == noConfigFlag || flag == noConfigFlag+"=true":
			noConfig = true
		case flag == "-c" || flag == configFlag:
			index++
			if index == len(flags) || flags[index] == "" || strings.HasPrefix(flags[index], "-") {
				return "", nil, failure(flag, errConfigPath)
			}

			path = flags[index]
		case strings.HasPrefix(flag, configFlag+"="):
			path = strings.TrimPrefix(flag, configFlag+"=")
			if path == "" {
				return "", nil, failure(configFlag, errConfigPath)
			}
		case strings.HasPrefix(flag, "-c="):
			path = strings.TrimPrefix(flag, "-c=")
			if path == "" {
				return "", nil, failure("-c", errConfigPath)
			}
		case strings.HasPrefix(flag, "-c"):
			path = strings.TrimPrefix(flag, "-c")
		default:
			remaining = append(remaining, flag)
		}
	}

	if noConfig && path != "" {
		return "", nil, errConfigFlags
	}

	if noConfig || path != "" {
		return path, remaining, nil
	}

	path, err := findConfiguration()

	return path, remaining, err
}

func findConfiguration() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", failure("resolve configuration directory", err)
	}

	for {
		for _, name := range []string{".golangci.yml", ".golangci.yaml", ".golangci.json"} {
			candidate := filepath.Join(directory, name)
			_, err := os.Stat(candidate)
			if err == nil {
				return candidate, nil
			}

			if !errors.Is(err, os.ErrNotExist) {
				return "", failure("find golangci-lint configuration", err)
			}
		}

		parent := filepath.Dir(directory)
		if parent == directory {
			return "", errConfigMissing
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
