// Native gate-token resolver for go-mk. The baseline gates compare a
// caller-supplied token against a rotating daily token. This file fetches that
// daily token in process over HTTP and parses the JSON, so no curl or jq is
// needed. The `gate-token` subcommand prints the slugified token for baseline
// maintenance. The same resolver feeds the gate checks, so the printed token
// and the checked token are slugified by one function and always agree.
package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"goodkind.io/go-makefile/internal/lint"
)

// gateTokenURL is the Wikipedia featured-feed endpoint for the current UTC date.
// The day path is filled in at call time.
const gateTokenURL = "https://en.wikipedia.org/api/rest_v1/feed/featured/"

// gateTokenUserAgent identifies the client to the Wikimedia API, which rejects
// the default Go user agent with HTTP 403.
const gateTokenUserAgent = "go-mk/1.0 (https://goodkind.io/go-makefile)"

// gateTokenFeed is the slice of the featured-feed JSON the token reads: the
// canonical title of the day's featured article.
type gateTokenFeed struct {
	TFA struct {
		Titles struct {
			Canonical string `json:"canonical"`
		} `json:"titles"`
	} `json:"tfa"`
}

// gateTokenRaw fetches the day's featured-article canonical title over HTTP and
// returns it unslugified. It returns the empty string and false on any network
// or parse failure, so a gate that cannot reach the feed stays closed. It runs a
// network request, so it emits a boundary log.
func gateTokenRaw() (string, bool) {
	day := time.Now().UTC().Format("2006/01/02")
	slog.Info("gate fetch token", slog.String("day", day))
	request, err := http.NewRequest(http.MethodGet, gateTokenURL+day, nil)
	if err != nil {
		return "", false
	}
	// Wikimedia rejects the default Go user agent with HTTP 403, so identify the
	// client with a contact URL per their API etiquette.
	request.Header.Set("User-Agent", gateTokenUserAgent)
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", false
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", false
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", false
	}
	var feed gateTokenFeed
	if err := json.Unmarshal(body, &feed); err != nil {
		return "", false
	}
	if feed.TFA.Titles.Canonical == "" {
		return "", false
	}
	return feed.TFA.Titles.Canonical, true
}

// dailyTokenSlug returns the slugified daily token, reading it from a per-UTC-day
// cache file under .make when present so repeated same-day builds make no
// network call. On a cache miss it fetches the title, slugifies it, writes the
// slug to the cache, and returns it. Only the slug is cached; the raw title
// never touches disk. It returns the empty string and false when the token
// cannot be resolved.
func dailyTokenSlug() (string, bool) {
	day := time.Now().UTC().Format("2006-01-02")
	cachePath := filepath.Join(makeDir, "gate-token-"+day)
	if cached, err := os.ReadFile(cachePath); err == nil {
		if slug := strings.TrimSpace(string(cached)); slug != "" {
			return slug, true
		}
	}
	raw, ok := gateTokenRaw()
	if !ok {
		return "", false
	}
	slug := lint.Slugify(raw)
	if slug == "" {
		return "", false
	}
	if err := os.MkdirAll(makeDir, 0o755); err == nil {
		slog.Info("gate write token cache", slog.String("day", day))
		_ = os.WriteFile(cachePath, []byte(slug+"\n"), 0o644)
	}
	return slug, true
}

// gateTokenSlug returns the slugified daily token for baseline maintenance. It
// returns the empty string and false when the token cannot be resolved.
func gateTokenSlug() (string, bool) {
	return dailyTokenSlug()
}

// runGateToken prints the slugified daily token for baseline maintenance. It
// returns 0 on success and 1 when the token cannot be resolved.
func runGateToken() int {
	slug, ok := gateTokenSlug()
	if !ok {
		writeStderr("go-mk gate-token: could not resolve the daily token\n")
		return 1
	}
	writeStdout(slug + "\n")
	return 0
}

// gateTokenExpected resolves the expected token for a baseline gate check. An
// explicit token command overrides the native resolver and returns its raw
// output; otherwise the cached daily slug is returned. gate.TokensMatch
// slugifies both sides, and slugifying a slug yields the same slug, so returning
// the slug compares correctly and the same-day cache keeps the gate off the
// network.
func gateTokenExpected(commandOverride string) (string, bool) {
	if commandOverride != "" {
		return runTokenCommand(commandOverride)
	}
	return dailyTokenSlug()
}
