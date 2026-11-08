package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var releaseTag = regexp.MustCompile(`^staticcheck/v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

func main() {
	slog.Info("publish analyzer command")
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: publish-staticcheck <main commit>")
		os.Exit(1)
	}
	version, err := publish(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("version=%s\n", version)
}

func publish(revision string) (string, error) {
	slog.Info("publish analyzer", slog.String("revision", revision))
	if _, err := os.Stat("staticcheck/go.mod"); err != nil {
		slog.Error("read analyzer module failed", slog.Any("err", err))
		return "", fmt.Errorf("run from the repository root: %w", err)
	}
	if _, err := gitOutput("fetch", "--tags", "origin"); err != nil {
		return "", err
	}
	target, err := gitOutput("rev-parse", "--verify", revision+"^{commit}")
	if err != nil {
		return "", err
	}
	mainBase, err := gitOutput("merge-base", target, "origin/main")
	if err != nil {
		return "", err
	}
	if mainBase != target {
		return "", fmt.Errorf("release target %s is not on origin/main", target)
	}
	tags, err := gitOutput("tag", "--list", "staticcheck/v*", "--sort=-version:refname")
	if err != nil {
		return "", err
	}
	for _, tag := range strings.Fields(tags) {
		parts := releaseTag.FindStringSubmatch(tag)
		if parts == nil {
			continue
		}
		version := strings.TrimPrefix(tag, "staticcheck/")
		base, err := gitOutput("merge-base", tag, target)
		if err != nil {
			return "", err
		}
		previous, err := gitOutput("rev-parse", tag+"^{commit}")
		if err != nil {
			return "", err
		}
		if base == target {
			return version, nil
		}
		if base != previous {
			return "", fmt.Errorf("release target %s diverges from %s", target, tag)
		}
		changed, err := gitOutput("diff", "--name-only", tag, target, "--", "staticcheck")
		if err != nil {
			return "", err
		}
		if changed == "" {
			return version, nil
		}
		patch, err := strconv.Atoi(parts[3])
		if err != nil {
			slog.Error("read analyzer patch version failed", slog.Any("err", err))
			return "", fmt.Errorf("invalid patch version in %s: %w", tag, err)
		}
		version = fmt.Sprintf("v%s.%s.%d", parts[1], parts[2], patch+1)
		next := "staticcheck/" + version
		command := exec.Command("git", "push", "origin", target+":refs/tags/"+next)
		command.Stdout = os.Stderr
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			slog.Error("publish analyzer tag failed", slog.Any("err", err))
			return "", fmt.Errorf("publish %s: %w", next, err)
		}
		return version, nil
	}
	return "", fmt.Errorf("publish an initial staticcheck/vX.Y.Z tag before enabling automatic releases")
}

func gitOutput(args ...string) (string, error) {
	slog.Info("publish analyzer git", slog.String("operation", args[0]))
	command := exec.Command("git", args...)
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		slog.Error("publish analyzer git failed", slog.Any("err", err))
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(output)), nil
}
