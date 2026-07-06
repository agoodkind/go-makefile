package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"goodkind.io/go-makefile/selfupdate"
)

type installerOptions struct {
	repo               string
	binary             string
	binDir             string
	version            string
	requireAttestation bool
	postInstallArgs    []string
}

var (
	resolveReleaseTagFunc    = selfupdate.ResolveReleaseTag
	installReleaseBinaryFunc = selfupdate.InstallReleaseBinary
	execInstalledBinaryFunc  = execInstalledBinary
)

func main() {
	slog.Info("go-mk-install invoked")
	os.Exit(runInstaller(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func runInstaller(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	options, err := parseInstallerOptions(args, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "go-mk-install: %v\n", err)
		return 1
	}
	resolvedTag, err := resolveReleaseTagFunc(ctx, selfupdateOptions(options), options.version, selfupdate.ReleaseChannelRolling)
	if err != nil {
		fmt.Fprintf(stderr, "go-mk-install: %v\n", err)
		return 1
	}
	result, err := installReleaseBinaryFunc(ctx, selfupdate.InstallReleaseBinaryOptions{
		Options: selfupdateOptions(options),
		Version: resolvedTag,
		Channel: selfupdate.ReleaseChannelRolling,
		BinDir:  options.binDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "go-mk-install: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "installed: %s\n", result.InstallPath)
	fmt.Fprintf(stdout, "tag: %s\n", result.Tag)
	fmt.Fprintf(stdout, "asset: %s\n", result.AssetName)
	if options.requireAttestation {
		fmt.Fprintln(stdout, "attestation: required")
	}
	if len(options.postInstallArgs) == 0 {
		return 0
	}
	if err := execInstalledBinaryFunc(result.InstallPath, options.postInstallArgs); err != nil {
		fmt.Fprintf(stderr, "go-mk-install: %v\n", err)
		return 1
	}
	return 0
}

func parseInstallerOptions(args []string, stderr io.Writer) (installerOptions, error) {
	options := installerOptions{binDir: defaultInstallerBinDir()}
	flagSet := flag.NewFlagSet("go-mk-install", flag.ContinueOnError)
	flagSet.SetOutput(stderr)
	flagSet.StringVar(&options.repo, "repo", "", "GitHub repository in OWNER/NAME form")
	flagSet.StringVar(&options.binary, "binary", "", "release binary name")
	flagSet.StringVar(&options.binDir, "bin-dir", options.binDir, "directory to install the binary into")
	flagSet.StringVar(&options.version, "version", "", "exact release tag to install")
	flagSet.BoolVar(&options.requireAttestation, "require-attestation", false, "require release attestations")
	if err := flagSet.Parse(args); err != nil {
		return installerOptions{}, err
	}
	options.postInstallArgs = append([]string{}, flagSet.Args()...)
	if strings.TrimSpace(options.repo) == "" {
		return installerOptions{}, fmt.Errorf("--repo is required")
	}
	if strings.TrimSpace(options.binary) == "" {
		return installerOptions{}, fmt.Errorf("--binary is required")
	}
	if strings.TrimSpace(options.binDir) == "" {
		return installerOptions{}, fmt.Errorf("--bin-dir is required")
	}
	return options, nil
}

func selfupdateOptions(options installerOptions) selfupdate.Options {
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GH_TOKEN"))
	}
	return selfupdate.Options{
		Config: selfupdate.Config{
			Repo:      options.repo,
			Binary:    options.binary,
			AuthToken: token,
		},
	}
}

func defaultInstallerBinDir() string {
	if xdgBinHome := strings.TrimSpace(os.Getenv("XDG_BIN_HOME")); xdgBinHome != "" {
		return xdgBinHome
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "bin")
	}
	return filepath.Join(".local", "bin")
}

func execInstalledBinary(path string, args []string) error {
	argv := make([]string, 0, len(args)+1)
	argv = append(argv, path)
	argv = append(argv, args...)
	return syscall.Exec(path, argv, os.Environ())
}
