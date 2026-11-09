package selfupdate_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"goodkind.io/go-makefile/selfupdate"
)

func newReleaseServer(t *testing.T, archives []string) *httptest.Server {
	t.Helper()
	checksums := strings.Builder{}

	for _, name := range archives {
		checksums.WriteString("deadbeef  " + name + "\n")
	}

	server := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/agoodkind/agent-gate/releases/download/v1.2.3/checksums.txt" {
				http.NotFound(writer, request)
				return
			}

			_, _ = writer.Write([]byte(checksums.String()))
		}),
	)
	t.Cleanup(server.Close)

	return server
}

func requiredAssetOptions(
	server *httptest.Server,
	t *testing.T,
	required []string,
) selfupdate.Options {
	t.Helper()

	return selfupdate.Options{
		Config: selfupdate.Config{
			Repo:       "agoodkind/agent-gate",
			Binary:     "agent-gate",
			WebBaseURL: server.URL,
		},
		Client:         server.Client(),
		CacheDir:       t.TempDir(),
		RequiredAssets: required,
	}
}

func TestVerifyReleaseAssetsRequiresEveryListedAsset(t *testing.T) {
	server := newReleaseServer(t, []string{"agent-gate_linux_amd64.tar.gz"})
	options := requiredAssetOptions(
		server,
		t,
		[]string{"agent-gate_linux_amd64.tar.gz", "agent-gate_linux_arm64.tar.gz"},
	)

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
	server := newReleaseServer(t, []string{})
	options := requiredAssetOptions(
		server,
		t,
		[]string{"agent-gate_linux_amd64.tar.gz", "agent-gate_linux_arm64.tar.gz"},
	)

	err := selfupdate.VerifyReleaseAssets(context.Background(), options, "v1.2.3")
	if err == nil {
		t.Fatal("VerifyReleaseAssets() error = nil, want missing required assets error")
	}

	if !strings.Contains(err.Error(), "lacks required assets") {
		t.Fatalf("VerifyReleaseAssets() error = %v, want the required assets error", err)
	}

	if strings.Contains(err.Error(), "no release assets matched") {
		t.Fatalf(
			"VerifyReleaseAssets() error = %v, want the required assets error before the binary error",
			err,
		)
	}

	for _, name := range options.RequiredAssets {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("VerifyReleaseAssets() error = %v, want it to list %s", err, name)
		}
	}
}
