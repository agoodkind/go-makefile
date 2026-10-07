package selfupdate_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"goodkind.io/go-makefile/selfupdate"
)

func TestVerifyReleaseAssetsRequiresEveryListedAsset(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/agoodkind/agent-gate/releases/tags/v1.2.3" {
			http.NotFound(writer, request)
			return
		}
		response := map[string]any{
			"tag_name": "v1.2.3",
			"assets": []map[string]any{
				{
					"name":                 "agent-gate_linux_amd64.tar.gz",
					"browser_download_url": server.URL + "/downloads/agent-gate_linux_amd64.tar.gz",
				},
			},
		}
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	options := selfupdate.Options{
		Config: selfupdate.Config{
			Repo:           "agoodkind/agent-gate",
			Binary:         "agent-gate",
			APIBaseURL:     server.URL,
			RequiredAssets: []string{"agent-gate_linux_amd64.tar.gz", "agent-gate_linux_arm64.tar.gz"},
		},
		Client:   server.Client(),
		CacheDir: t.TempDir(),
	}

	err := selfupdate.VerifyReleaseAssets(context.Background(), options, "v1.2.3")
	if err == nil {
		t.Fatal("VerifyReleaseAssets() error = nil, want missing required asset error")
	}
	if !strings.Contains(err.Error(), "agent-gate_linux_arm64.tar.gz") {
		t.Fatalf("VerifyReleaseAssets() error = %v, want the missing arm64 archive", err)
	}
	if strings.Contains(err.Error(), "agent-gate_linux_amd64.tar.gz") {
		t.Fatalf("VerifyReleaseAssets() error = %v, reports a present archive as missing", err)
	}
}

func TestVerifyReleaseAssetsReportsRequiredAssetsBeforeTheBinaryCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/agoodkind/agent-gate/releases/tags/v1.2.3" {
			http.NotFound(writer, request)
			return
		}
		response := map[string]any{"tag_name": "v1.2.3", "assets": []map[string]any{}}
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	options := selfupdate.Options{
		Config: selfupdate.Config{
			Repo:           "agoodkind/agent-gate",
			Binary:         "agent-gate",
			APIBaseURL:     server.URL,
			RequiredAssets: []string{"agent-gate_linux_amd64.tar.gz", "agent-gate_linux_arm64.tar.gz"},
		},
		Client:   server.Client(),
		CacheDir: t.TempDir(),
	}

	err := selfupdate.VerifyReleaseAssets(context.Background(), options, "v1.2.3")
	if err == nil {
		t.Fatal("VerifyReleaseAssets() error = nil, want missing required assets error")
	}
	if !strings.Contains(err.Error(), "lacks required assets") {
		t.Fatalf("VerifyReleaseAssets() error = %v, want the required assets error", err)
	}
	if strings.Contains(err.Error(), "no release assets matched") {
		t.Fatalf("VerifyReleaseAssets() error = %v, want the required assets error before the binary error", err)
	}
	for _, name := range options.Config.RequiredAssets {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("VerifyReleaseAssets() error = %v, want it to list %s", err, name)
		}
	}
}

func TestVerifyReleaseAssetsReportsMissingDownloadURLForRequiredAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/agoodkind/agent-gate/releases/tags/v1.2.3" {
			http.NotFound(writer, request)
			return
		}
		response := map[string]any{
			"tag_name": "v1.2.3",
			"assets":   []map[string]any{{"name": "agent-gate_linux_amd64.tar.gz"}},
		}
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	options := selfupdate.Options{
		Config: selfupdate.Config{
			Repo:           "agoodkind/agent-gate",
			Binary:         "agent-gate",
			APIBaseURL:     server.URL,
			RequiredAssets: []string{"agent-gate_linux_amd64.tar.gz"},
		},
		Client:   server.Client(),
		CacheDir: t.TempDir(),
	}

	err := selfupdate.VerifyReleaseAssets(context.Background(), options, "v1.2.3")
	if err == nil {
		t.Fatal("VerifyReleaseAssets() error = nil, want missing download URL error")
	}
	if strings.Contains(err.Error(), "lacks required assets") {
		t.Fatalf("VerifyReleaseAssets() error = %v, reports a present asset as missing", err)
	}
	if !strings.Contains(err.Error(), "has no download URL") {
		t.Fatalf("VerifyReleaseAssets() error = %v, want the download URL error", err)
	}
}
