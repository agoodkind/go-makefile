package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// downloadAttempts bounds the attempts for one download, the first included.
const downloadAttempts = 3

// downloadRetryDelay is the wait before the second attempt. Each later wait
// doubles it.
var downloadRetryDelay = 2 * time.Second

func downloadReleaseAsset(ctx context.Context, options Options, asset releaseAsset, path string) error {
	return updateDownloadFile(
		ctx,
		options.Client,
		asset.BrowserDownloadURL,
		path,
		options.Config.MaxDownloadBytes,
	)
}

// releaseAssetURL returns the direct download URL of one release asset.
func releaseAssetURL(cfg Config, tag string, name string) string {
	return releaseWebBaseURL(cfg) + "/" + cfg.Repo + "/releases/download/" + neturl.PathEscape(tag) + "/" + neturl.PathEscape(name)
}

// downloadFile writes url to path, refusing anything larger than maxBytes so a
// hostile or corrupt asset cannot exhaust the disk. The caller supplies the
// limit because a plausible asset size is a property of the consumer's binary,
// not of this package. It retries an HTTP 5xx response or a network error up to
// downloadAttempts times in total, doubling the delay after each failure. It
// returns any other failure, including every HTTP 4xx response, without a
// retry. Callers verify the checksum of the file it writes.
func downloadFile(ctx context.Context, client *http.Client, url string, path string, maxBytes int64) error {
	delay := downloadRetryDelay
	for attempt := 1; ; attempt++ {
		retryable, err := downloadFileOnce(ctx, client, url, path, maxBytes)
		if err == nil {
			return nil
		}
		if !retryable || attempt >= downloadAttempts {
			return err
		}
		slog.WarnContext(ctx, "update download retrying", "url", url, "attempt", attempt, "delay", delay, "err", err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			slog.WarnContext(ctx, "update download retry canceled", "url", url, "err", ctx.Err())
			return fmt.Errorf("download %s: %w", url, ctx.Err())
		case <-timer.C:
		}
		delay *= 2
	}
}

// readErrorRecorder records the first error other than io.EOF that its reader
// returns.
type readErrorRecorder struct {
	reader io.Reader
	err    error
}

func (recorder *readErrorRecorder) Read(buffer []byte) (int, error) {
	count, err := recorder.reader.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) && recorder.err == nil {
		recorder.err = err
	}
	return count, err
}

// downloadFileOnce makes one download attempt. The boolean reports whether
// the failure is an HTTP 5xx response or a network error.
func downloadFileOnce(
	ctx context.Context,
	client *http.Client,
	url string,
	path string,
	maxBytes int64,
) (bool, error) {
	slog.InfoContext(ctx, "update download file", "url", url, "path", path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		slog.WarnContext(ctx, "update download request build failed", "url", url, "err", err)
		return false, fmt.Errorf("build download request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "update download request failed", "url", url, "err", err)
		return ctx.Err() == nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
		slog.WarnContext(ctx, "update download status failed", "url", url, "status_code", resp.StatusCode, "err", err)
		return resp.StatusCode >= http.StatusInternalServerError, err
	}
	if resp.ContentLength > maxBytes {
		err := fmt.Errorf("download %s exceeds %d bytes", url, maxBytes)
		slog.WarnContext(ctx, "update download size rejected", "url", url, "content_length", resp.ContentLength, "err", err)
		return false, err
	}
	out, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		slog.WarnContext(ctx, "update download temp open failed", "path", path, "err", err)
		return false, fmt.Errorf("open download temp: %w", err)
	}
	tmpPath := out.Name()
	body := &readErrorRecorder{reader: io.LimitReader(resp.Body, maxBytes+1)}
	written, copyErr := io.Copy(out, body)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		slog.WarnContext(ctx, "update download copy failed", "path", path, "err", copyErr)
		// A response body read error is a network error. A temp file write
		// error is a local file error and is not retried.
		if body.err != nil {
			return ctx.Err() == nil, fmt.Errorf("read download %s: %w", url, copyErr)
		}
		return false, fmt.Errorf("write download temp: %w", copyErr)
	}
	if written > maxBytes {
		_ = os.Remove(tmpPath)
		err := fmt.Errorf("download %s exceeds %d bytes", url, maxBytes)
		slog.WarnContext(ctx, "update download size exceeded", "url", url, "written", written, "err", err)
		return false, err
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		slog.WarnContext(ctx, "update download close failed", "path", path, "err", closeErr)
		return false, fmt.Errorf("close download temp: %w", closeErr)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		slog.WarnContext(ctx, "update download replace failed", "path", path, "err", err)
		return false, fmt.Errorf("replace download: %w", err)
	}
	return false, nil
}

func verifyChecksum(ctx context.Context, options Options, latest release, asset releaseAsset, archivePath string) error {
	checksums, ok := findAsset(latest.Assets, checksumsAssetName)
	if !ok {
		return fmt.Errorf("checksum unavailable for %s", asset.Name)
	}
	// Download checksums.txt once per release because a multi-binary release
	// verifies many archives against one file. The cache file name hashes the
	// repo and release tag because CacheDir is stable per binary name. A file
	// from another release or another repo has a different cache file name.
	checksumsPath := filepath.Join(options.CacheDir, "checksums-"+checksumsCacheKey(options.Config.Repo, latest.TagName)+".txt")
	if _, statErr := os.Stat(checksumsPath); statErr != nil {
		if err := downloadReleaseAsset(ctx, options, checksums, checksumsPath); err != nil {
			return err
		}
	}
	want, err := checksumFromFile(checksumsPath, asset.Name)
	if err != nil {
		return err
	}
	got, err := sha256File(archivePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(want, got) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", asset.Name, want, got)
	}
	return nil
}

// checksumsCacheKey derives a filesystem-safe, collision-free cache-file key
// for one (repo, tag) pair by hashing the pair. Hashing sidesteps sanitization
// ambiguity entirely: distinct inputs can never map to the same key the way a
// character-replacement scheme would (for example org/repo vs org-repo).
func checksumsCacheKey(repo string, tag string) string {
	digest := sha256.Sum256([]byte(repo + "@" + tag))
	return hex.EncodeToString(digest[:8])
}

// checksumEntryName removes the ./ prefix that checksums.txt in older releases
// writes before each file name.
func checksumEntryName(entry string) string {
	return strings.TrimPrefix(entry, "./")
}

func checksumFromFile(path string, name string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("update checksums read failed", "path", path, "err", err)
		return "", fmt.Errorf("read checksums: %w", err)
	}
	for line := range strings.SplitSeq(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if checksumEntryName(fields[1]) == name {
			return fields[0], nil
		}
	}
	err = fmt.Errorf("checksum entry not found for %s", name)
	slog.Warn("update checksums entry missing", "path", path, "name", name, "err", err)
	return "", err
}

func sha256File(path string) (string, error) {
	slog.Info("update hash file", "path", path)
	file, err := os.Open(path)
	if err != nil {
		slog.Warn("update checksum input open failed", "path", path, "err", err)
		return "", fmt.Errorf("open checksum input: %w", err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		slog.Warn("update checksum input hash failed", "path", path, "err", err)
		return "", fmt.Errorf("hash checksum input: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
