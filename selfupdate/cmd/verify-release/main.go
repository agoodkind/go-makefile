package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"

	"goodkind.io/go-makefile/selfupdate"
)

type releaseVerifier func(context.Context, selfupdate.Options, string) error

// archiveFlagNames lists the flags that only archive mode accepts.
var archiveFlagNames = []string{"archive", "asset", "sha256", "source-revision", "source-ref", "signer-workflow"}

// downloadFlagNames lists the flags that only download mode accepts.
var downloadFlagNames = []string{"binary", "required-assets"}

func main() {
	slog.Info("verify release command start")
	os.Exit(runVerifyRelease(context.Background(), os.Args[1:], os.Stdout, os.Stderr, selfupdate.VerifyReleaseAssets))
}

func runVerifyRelease(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	verify releaseVerifier,
) int {
	flagSet := flag.NewFlagSet("verify-release", flag.ContinueOnError)
	flagSet.SetOutput(stderr)
	repo := flagSet.String("repo", "", "GitHub repository as OWNER/NAME")
	tag := flagSet.String("tag", "", "release tag to verify")
	binary := flagSet.String("binary", "", "release binary name")
	apiBaseURL := flagSet.String("api-base", "", "GitHub API base URL")
	requiredAssets := flagSet.String("required-assets", "", "space-separated asset file names required in the release")
	archive := flagSet.String("archive", "", "local release archive to verify; selects archive mode")
	asset := flagSet.String("asset", "", "release asset name of the local archive (archive mode)")
	sha256 := flagSet.String("sha256", "", "expected SHA-256 of the local archive (archive mode)")
	sourceRevision := flagSet.String("source-revision", "", "expected source commit of the build (archive mode)")
	sourceRef := flagSet.String("source-ref", "", "expected source ref of the build, such as refs/heads/main (archive mode)")
	signerWorkflow := flagSet.String("signer-workflow", "", "exact signer workflow URI of the build (archive mode)")
	if err := flagSet.Parse(args); err != nil {
		return 1
	}
	if *repo == "" {
		fmt.Fprintln(stderr, "--repo is required")
		return 1
	}
	if *tag == "" {
		fmt.Fprintln(stderr, "--tag is required")
		return 1
	}
	setFlags := map[string]bool{}
	flagSet.Visit(func(setFlag *flag.Flag) {
		setFlags[setFlag.Name] = true
	})
	if *archive != "" {
		if rejected := firstSetFlag(setFlags, downloadFlagNames); rejected != "" {
			fmt.Fprintf(stderr, "--%s is not valid with --archive\n", rejected)
			return 1
		}
		archiveOptions := selfupdate.ArtifactVerificationOptions{
			Options: selfupdate.Options{
				Config: selfupdate.Config{
					Repo:              *repo,
					SignerWorkflowURI: *signerWorkflow,
					APIBaseURL:        *apiBaseURL,
					AuthToken:         os.Getenv("GITHUB_TOKEN"),
				},
				Log: slog.New(slog.NewTextHandler(stderr, nil)),
			},
			ReleaseTag:     *tag,
			AssetName:      *asset,
			SHA256:         *sha256,
			SourceRevision: *sourceRevision,
			SourceRef:      *sourceRef,
		}
		return runVerifyArchive(ctx, *archive, archiveOptions, stdout, stderr)
	}
	if rejected := firstSetFlag(setFlags, archiveFlagNames); rejected != "" {
		fmt.Fprintf(stderr, "--%s requires --archive\n", rejected)
		return 1
	}
	if *binary == "" {
		fmt.Fprintln(stderr, "--binary is required")
		return 1
	}
	counter := atomic.Int64{}
	handler := &verifiedAssetCounterHandler{
		delegate: slog.NewTextHandler(stderr, nil),
		count:    &counter,
	}
	options := selfupdate.Options{
		Config: selfupdate.Config{
			Repo:       *repo,
			Binary:     *binary,
			APIBaseURL: *apiBaseURL,
			AuthToken:  os.Getenv("GITHUB_TOKEN"),
		},
		Log:            slog.New(handler),
		RequiredAssets: strings.Fields(*requiredAssets),
	}
	if err := verify(ctx, options, *tag); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "verified %d assets for %s\n", counter.Load(), *tag)
	return 0
}

func runVerifyArchive(
	ctx context.Context,
	archivePath string,
	options selfupdate.ArtifactVerificationOptions,
	stdout io.Writer,
	stderr io.Writer,
) int {
	archive, err := os.Open(archivePath)
	if err != nil {
		fmt.Fprintf(stderr, "error: open archive: %v\n", err)
		return 1
	}
	defer func() { _ = archive.Close() }()
	result, err := selfupdate.VerifyLocalArtifact(ctx, options, archive)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "verified %s from %s %s\n", result.AssetName, result.Repository, result.ReleaseTag)
	fmt.Fprintf(stdout, "sha256 %s\n", result.SHA256)
	fmt.Fprintf(stdout, "signer workflow %s\n", result.SignerWorkflowURI)
	fmt.Fprintf(stdout, "source revision %s\n", result.SourceRevision)
	fmt.Fprintf(stdout, "source ref %s\n", result.SourceRef)
	return 0
}

func firstSetFlag(setFlags map[string]bool, names []string) string {
	for _, name := range names {
		if setFlags[name] {
			return name
		}
	}
	return ""
}

type verifiedAssetCounterHandler struct {
	delegate slog.Handler
	count    *atomic.Int64
}

func (handler *verifiedAssetCounterHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.delegate.Enabled(ctx, level)
}

func (handler *verifiedAssetCounterHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == "release asset verified" {
		handler.count.Add(1)
	}
	return handler.delegate.Handle(ctx, record)
}

func (handler *verifiedAssetCounterHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &verifiedAssetCounterHandler{
		delegate: handler.delegate.WithAttrs(attrs),
		count:    handler.count,
	}
}

func (handler *verifiedAssetCounterHandler) WithGroup(name string) slog.Handler {
	return &verifiedAssetCounterHandler{
		delegate: handler.delegate.WithGroup(name),
		count:    handler.count,
	}
}
