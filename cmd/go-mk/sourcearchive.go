package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	sourceArchiveName     = "go-makefile-src.tar.gz"
	rollingReleaseTagName = "rolling"
)

// sourceArchiveMembers is the fixed set of consumer-facing assets the source
// tarball carries. It is not a full git export.
var sourceArchiveMembers = []string{
	"go.mk",
	"go-build.mk",
	"go-release.mk",
	"go-service.mk",
	"bootstrap.mk",
	"golangci.yml",
	"notices.txt",
	"scripts/go-mk-bootstrap.sh",
	"hooks/pre-commit",
}

var shellFullLineCommentPattern = regexp.MustCompile(`^[ \t]*#`)

// stripShellFullLineComments removes full-line # comments from shell source.
// The shebang line is preserved. Inline comments inside quoted strings are not
// stripped because that would require a shell parser.
func stripShellFullLineComments(content []byte) []byte {
	lines := strings.Split(string(content), "\n")
	output := make([]string, 0, len(lines))
	for index, line := range lines {
		if index == 0 && strings.HasPrefix(line, "#!") {
			output = append(output, line)
			continue
		}
		if shellFullLineCommentPattern.MatchString(line) {
			continue
		}
		output = append(output, line)
	}
	joined := strings.Join(output, "\n")
	if len(content) > 0 && content[len(content)-1] == '\n' && !strings.HasSuffix(joined, "\n") {
		joined += "\n"
	}
	return []byte(joined)
}

func sourceArchiveMemberMode(member string) int64 {
	if strings.HasSuffix(member, ".sh") {
		return 0o755
	}
	return 0o644
}

func prepareSourceArchiveMemberContent(member string, raw []byte) ([]byte, error) {
	if !strings.HasSuffix(member, ".sh") {
		return raw, nil
	}
	stripped := stripShellFullLineComments(raw)
	if err := validateShellSyntax(member, stripped); err != nil {
		return nil, err
	}
	return stripped, nil
}

func validateShellSyntax(member string, content []byte) error {
	tempFile, err := os.CreateTemp("", "go-mk-shell-syntax-*.sh")
	if err != nil {
		slog.Error("release create temp shell file failed", slog.String("member", member), slog.Any("err", err))
		return fmt.Errorf("release: create temp shell file for %s: %w", member, err)
	}
	tempPath := tempFile.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err := tempFile.Write(content); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("release: write temp shell file for %s: %w", member, err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("release: close temp shell file for %s: %w", member, err)
	}
	cmd := exec.Command("bash", "-n", tempPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("release: bash -n rejected %s: %w\n%s", member, err, output)
	}
	return nil
}

// writeSourceArchive builds dist/go-makefile-src.tar.gz from the repository
// root. Each member is placed under a single top-level directory so consumers
// can extract with tar --strip-components 1.
func writeSourceArchive(distDir string) (string, error) {
	archivePath := filepath.Join(distDir, sourceArchiveName)
	slog.Info("release source archive", slog.String("path", archivePath), slog.Int("members", len(sourceArchiveMembers)))

	file, err := os.Create(archivePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, member := range sourceArchiveMembers {
		raw, err := os.ReadFile(member)
		if err != nil {
			slog.Error("release read source archive member failed", slog.String("member", member), slog.Any("err", err))
			return "", fmt.Errorf("release: read source archive member %s: %w", member, err)
		}
		content, err := prepareSourceArchiveMemberContent(member, raw)
		if err != nil {
			return "", err
		}
		header := &tar.Header{
			Name: filepath.ToSlash(filepath.Join("go-makefile-src", member)),
			Mode: sourceArchiveMemberMode(member),
			Size: int64(len(content)),
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return "", err
		}
		if _, err := tarWriter.Write(content); err != nil {
			return "", err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return "", err
	}
	if err := gzipWriter.Close(); err != nil {
		return "", err
	}
	return archivePath, nil
}

func releaseSourceArchiveEnabled() bool {
	return envTruthy(os.Getenv("RELEASE_SOURCE_ARCHIVE"))
}

func appendSourceArchiveIfEnabled(distDir string, archives []string) ([]string, error) {
	if !releaseSourceArchiveEnabled() {
		return archives, nil
	}
	slog.Info("release append source archive", slog.String("dir", distDir))
	archivePath, err := writeSourceArchive(distDir)
	if err != nil {
		return nil, err
	}
	return append(archives, archivePath), nil
}

// retargetRollingRelease moves the persistent rolling release to the published
// tag and replaces its assets with the freshly published set.
func retargetRollingRelease(cfg releaseConfig, assets []string) error {
	if !releaseSourceArchiveEnabled() {
		return nil
	}
	slog.Info("release retarget rolling", slog.String("tag", cfg.tag), slog.String("sha", cfg.targetSHA))
	repo := strings.TrimSpace(os.Getenv("GITHUB_REPOSITORY"))
	if repo == "" {
		repo = "agoodkind/go-makefile"
	}
	if err := deleteRollingRelease(repo); err != nil {
		return err
	}
	args := []string{
		"release", "create", rollingReleaseTagName,
		"--repo", repo,
		"--target", cfg.targetSHA,
		"--title", "go-makefile rolling",
		"--notes", "Rolling release for unpinned consumer fetch. Assets mirror tag " + cfg.tag + ".",
		"--prerelease",
	}
	args = append(args, assets...)
	return runProcess("gh", args, nil)
}

func deleteRollingRelease(repo string) error {
	slog.Info("release delete rolling", slog.String("repo", repo))
	args := []string{"release", "delete", rollingReleaseTagName, "--repo", repo, "--yes", "--cleanup-tag"}
	if err := runProcess("gh", args, nil); err != nil {
		if strings.Contains(err.Error(), "exit status") {
			// A missing rolling release is fine on first publish.
			return nil
		}
		return err
	}
	return nil
}

// extractSourceArchiveWithSystemTar mirrors consumer extraction.
func extractSourceArchiveWithSystemTar(archivePath string, extractDir string) error {
	slog.Info("extract source archive", slog.String("archive", archivePath), slog.String("dir", extractDir))
	cmd := exec.Command("tar", "-xzf", archivePath, "-C", extractDir, "--strip-components", "1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("tar extract failed", slog.String("archive", archivePath), slog.Any("err", err))
		return fmt.Errorf("tar extract: %w\n%s", err, output)
	}
	return nil
}
