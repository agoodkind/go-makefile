package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func downloadFile(ctx context.Context, client *http.Client, url string, path string) error {
	slog.InfoContext(ctx, "update download file", "url", url, "path", path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		slog.WarnContext(ctx, "update download request build failed", "url", url, "err", err)
		return fmt.Errorf("build download request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "update download request failed", "url", url, "err", err)
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
		slog.WarnContext(ctx, "update download status failed", "url", url, "status_code", resp.StatusCode, "err", err)
		return err
	}
	if resp.ContentLength > maxDownloadedAssetBytes {
		err := fmt.Errorf("download %s exceeds %d bytes", url, maxDownloadedAssetBytes)
		slog.WarnContext(ctx, "update download size rejected", "url", url, "content_length", resp.ContentLength, "err", err)
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		slog.WarnContext(ctx, "update download temp open failed", "path", path, "err", err)
		return fmt.Errorf("open download temp: %w", err)
	}
	tmpPath := out.Name()
	limitedReader := io.LimitReader(resp.Body, maxDownloadedAssetBytes+1)
	written, copyErr := io.Copy(out, limitedReader)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		slog.WarnContext(ctx, "update download copy failed", "path", path, "err", copyErr)
		return fmt.Errorf("write download temp: %w", copyErr)
	}
	if written > maxDownloadedAssetBytes {
		_ = os.Remove(tmpPath)
		err := fmt.Errorf("download %s exceeds %d bytes", url, maxDownloadedAssetBytes)
		slog.WarnContext(ctx, "update download size exceeded", "url", url, "written", written, "err", err)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		slog.WarnContext(ctx, "update download close failed", "path", path, "err", closeErr)
		return fmt.Errorf("close download temp: %w", closeErr)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		slog.WarnContext(ctx, "update download replace failed", "path", path, "err", err)
		return fmt.Errorf("replace download: %w", err)
	}
	return nil
}

func verifyChecksum(ctx context.Context, options Options, latest release, asset releaseAsset, archivePath string) error {
	want := checksumFromAsset(asset)
	if want == "" {
		checksums, ok := findAsset(latest.Assets, "checksums.txt")
		if !ok {
			return fmt.Errorf("checksum unavailable for %s", asset.Name)
		}
		// Cache checksums.txt once per release: a multi-binary release verifies
		// many archives, so re-downloading the shared checksums file per asset
		// would scale network requests as O(archives). The cache file is keyed
		// by release tag because CacheDir is stable per binary, so an unkeyed
		// file left by an earlier release would poison a later verification.
		checksumsPath := filepath.Join(options.CacheDir, "checksums-"+sanitizeCacheFileComponent(latest.TagName)+".txt")
		if _, statErr := os.Stat(checksumsPath); statErr != nil {
			if err := downloadFile(ctx, options.Client, checksums.BrowserDownloadURL, checksumsPath); err != nil {
				return err
			}
		}
		resolved, err := checksumFromFile(checksumsPath, asset.Name)
		if err != nil {
			return err
		}
		want = resolved
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

// sanitizeCacheFileComponent maps a release tag to a safe cache-file name
// component, replacing path separators and any character outside the tag
// alphabet with a dash.
func sanitizeCacheFileComponent(value string) string {
	mapped := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, value)
	if mapped == "" {
		return "untagged"
	}
	return mapped
}

func checksumFromAsset(asset releaseAsset) string {
	if digest, ok := strings.CutPrefix(asset.Digest, "sha256:"); ok {
		return digest
	}
	return ""
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
		if fields[1] == name {
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
