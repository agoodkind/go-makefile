package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"

	"goodkind.io/go-makefile/selfupdate"
)

type releaseVerifier func(context.Context, selfupdate.Options, string) error

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
		Log: slog.New(handler),
	}
	if err := verify(ctx, options, *tag); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "verified %d assets for %s\n", counter.Load(), *tag)
	return 0
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
