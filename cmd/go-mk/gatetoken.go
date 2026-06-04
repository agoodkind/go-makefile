// Native gate-token resolver for go-mk. The bypass and baseline gates compare a
// caller-supplied token against a rotating daily token. This file fetches that
// daily token in process over HTTP and parses the JSON, so no curl or jq is
// needed. The `gate-token` subcommand prints the slugified token for use as
// BYPASS_LINT or BASELINE_TOKEN. The same resolver feeds the gate checks, so the
// printed token and the checked token are slugified by one function and always
// agree.
package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
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

// gateTokenSlug returns the slugified daily token, the value a caller passes as
// BYPASS_LINT or BASELINE_TOKEN. It returns the empty string and false when the
// token cannot be resolved.
func gateTokenSlug() (string, bool) {
	raw, ok := gateTokenRaw()
	if !ok {
		return "", false
	}
	return lint.Slugify(raw), true
}

// runGateToken prints the slugified daily token, so a caller can run
// `BYPASS_LINT=$(go-mk gate-token) BYPASS_CONFIRM=1 make build`. It returns 0 on
// success and 1 when the token cannot be resolved.
func runGateToken() int {
	slug, ok := gateTokenSlug()
	if !ok {
		writeStderr("go-mk gate-token: could not resolve the daily token\n")
		return 1
	}
	writeStdout(slug + "\n")
	return 0
}

// gateTokenExpected resolves the expected token for a gate check. An explicit
// token command (BYPASS_TOKEN_CMD or BASELINE_TOKEN_CMD) overrides the native
// fetch; otherwise the native HTTP resolver is used. The returned value is raw,
// since the caller slugifies through gate.TokensMatch.
func gateTokenExpected(commandOverride string) (string, bool) {
	if commandOverride != "" {
		return runTokenCommand(commandOverride)
	}
	return gateTokenRaw()
}
