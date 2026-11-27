package lint

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type testShard struct {
	Name     string   `json:"name"`
	Packages []string `json:"packages"`
}

func ciTestTargets(name, definition string) ([]string, error) {
	var shards []testShard
	if err := json.Unmarshal([]byte(definition), &shards); err != nil {
		return nil, fmt.Errorf("decode CI test shards: %w", err)
	}

	command := exec.Command("go", "list", "./...")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list Go packages for CI test shards: %w", err)
	}

	targets, err := partitionTestPackages(strings.Fields(string(output)), shards)
	if err != nil {
		return nil, err
	}

	if len(targets[name]) == 0 {
		return nil, fmt.Errorf("CI test shard %q has no packages", name)
	}

	return targets[name], nil
}

func partitionTestPackages(packages []string, shards []testShard) (map[string][]string, error) {
	targets := make(map[string][]string, len(shards))
	var remainder string
	for _, shard := range shards {
		if _, exists := targets[shard.Name]; shard.Name == "" || exists {
			return nil, fmt.Errorf("invalid or repeated CI test shard %q", shard.Name)
		}
		targets[shard.Name] = nil
		if len(shard.Packages) == 0 {
			if remainder != "" {
				return nil, fmt.Errorf("CI test shards %q and %q both select the remaining packages", remainder, shard.Name)
			}
			remainder = shard.Name
		}
	}

	for _, path := range packages {
		var selected string
		for _, shard := range shards {
			if !matchesTestPackage(path, shard.Packages) {
				continue
			}
			if selected != "" {
				return nil, fmt.Errorf("Go package %q matches CI test shards %q and %q", path, selected, shard.Name)
			}
			selected = shard.Name
		}
		if selected == "" {
			selected = remainder
		}
		if selected == "" {
			return nil, fmt.Errorf("Go package %q is missing from the CI test shards", path)
		}
		targets[selected] = append(targets[selected], path)
	}

	for _, shard := range shards {
		if len(targets[shard.Name]) == 0 {
			return nil, fmt.Errorf("CI test shard %q has no packages", shard.Name)
		}
	}

	return targets, nil
}

func matchesTestPackage(path string, patterns []string) bool {
	for _, pattern := range patterns {
		prefix, recursive := strings.CutSuffix(pattern, "/...")
		if path == prefix || recursive && strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}
