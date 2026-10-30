package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"strings"
)

// ArtifactVerificationOptions selects a release asset and the identity that
// its local copy must match. It requires Options.Config.Repo,
// Options.Config.SignerWorkflowURI, ReleaseTag, AssetName, SHA256,
// SourceRevision, and SourceRef. VerifyLocalArtifact uses Options.Client,
// Options.CacheDir, Options.Log, and the GitHub API settings in
// Options.Config. It ignores the binary identity, install path, state path,
// and candidate validation settings.
type ArtifactVerificationOptions struct {
	Options        Options
	ReleaseTag     string
	AssetName      string
	SHA256         string
	SourceRevision string
	SourceRef      string
}

// ArtifactVerificationResult reports the repository, signer workflow URI,
// source revision, and source ref from the verified certificate, along with
// the release tag, asset name, and local SHA-256.
type ArtifactVerificationResult struct {
	Repository        string
	SignerWorkflowURI string
	SourceRevision    string
	SourceRef         string
	ReleaseTag        string
	AssetName         string
	SHA256            string
}

// VerifyLocalArtifact verifies artifact as the selected asset of a GitHub
// release. It hashes every byte of artifact and compares the digest with
// options.SHA256 and the published release checksum. It verifies the release
// attestation and the build provenance attestation for that digest. The
// release tag must resolve to options.SourceRevision. The build provenance
// certificate must match options.SourceRevision, options.SourceRef, and
// Options.Config.SignerWorkflowURI exactly.
//
// VerifyLocalArtifact reads artifact with ReadAt and leaves the file offset
// unchanged. It does not close, modify, extract, or download a copy of
// artifact.
func VerifyLocalArtifact(
	ctx context.Context,
	options ArtifactVerificationOptions,
	artifact *os.File,
) (ArtifactVerificationResult, error) {
	resolvedOptions := resolveOptions(options.Options)
	log := resolvedOptions.Log
	if err := validateArtifactVerificationInput(options, artifact); err != nil {
		log.WarnContext(ctx, "local artifact verification input rejected", "err", err)
		return ArtifactVerificationResult{}, err
	}
	digestHex, err := sha256OpenFile(artifact)
	if err != nil {
		log.WarnContext(ctx, "local artifact hash failed", "path", artifact.Name(), "err", err)
		return ArtifactVerificationResult{}, err
	}
	expectedDigest := strings.ToLower(options.SHA256)
	if digestHex != expectedDigest {
		err := fmt.Errorf("local artifact %s has SHA-256 %s, expected %s", artifact.Name(), digestHex, expectedDigest)
		log.WarnContext(ctx, "local artifact digest mismatch", "path", artifact.Name(), "err", err)
		return ArtifactVerificationResult{}, err
	}
	tagged, err := fetchReleaseByTag(ctx, resolvedOptions, options.ReleaseTag)
	if err != nil {
		return ArtifactVerificationResult{}, err
	}
	if tagged.TagName != options.ReleaseTag {
		return ArtifactVerificationResult{}, fmt.Errorf("release tag %q did not match %q", tagged.TagName, options.ReleaseTag)
	}
	asset, ok := findAsset(tagged.Assets, options.AssetName)
	if !ok {
		return ArtifactVerificationResult{}, fmt.Errorf("release %s does not contain asset %s", options.ReleaseTag, options.AssetName)
	}
	if err := verifyLocalArtifactChecksum(ctx, resolvedOptions, tagged, asset, digestHex); err != nil {
		log.WarnContext(ctx, "local artifact release checksum failed", "asset", asset.Name, "tag", tagged.TagName, "err", err)
		return ArtifactVerificationResult{}, err
	}
	digestBytes, err := hex.DecodeString(digestHex)
	if err != nil {
		return ArtifactVerificationResult{}, fmt.Errorf("decode artifact digest: %w", err)
	}
	repo := resolvedOptions.Config.Repo
	tagCommitSHA, err := resolveReleaseTagCommitSHA(ctx, resolvedOptions, repo, tagged.TagName)
	if err != nil {
		log.WarnContext(ctx, "local artifact release tag resolve failed", "repo", repo, "tag", tagged.TagName, "err", err)
		return ArtifactVerificationResult{}, err
	}
	if tagCommitSHA != options.SourceRevision {
		err := fmt.Errorf("release tag %s resolves to commit %s, expected %s", tagged.TagName, tagCommitSHA, options.SourceRevision)
		log.WarnContext(ctx, "local artifact release commit mismatch", "repo", repo, "tag", tagged.TagName, "err", err)
		return ArtifactVerificationResult{}, err
	}
	if err := verifyReleaseAttestationAtCommit(ctx, resolvedOptions, tagged, asset, tagCommitSHA, digestHex, digestBytes); err != nil {
		log.WarnContext(ctx, "local artifact release attestation failed", "asset", asset.Name, "tag", tagged.TagName, "err", err)
		return ArtifactVerificationResult{}, err
	}
	source := buildProvenanceSource{Revision: options.SourceRevision, Ref: options.SourceRef}
	certificate, err := verifyBuildProvenanceFromSource(ctx, resolvedOptions, asset, digestHex, digestBytes, source)
	if err != nil {
		log.WarnContext(ctx, "local artifact build provenance failed", "asset", asset.Name, "repo", repo, "err", err)
		return ArtifactVerificationResult{}, err
	}
	result := ArtifactVerificationResult{
		Repository:        repo,
		SignerWorkflowURI: certificate.BuildSignerURI,
		SourceRevision:    certificate.SourceRepositoryDigest,
		SourceRef:         certificate.SourceRepositoryRef,
		ReleaseTag:        tagged.TagName,
		AssetName:         asset.Name,
		SHA256:            digestHex,
	}
	log.InfoContext(ctx, "local artifact verified", "asset", result.AssetName, "tag", result.ReleaseTag, "sha256", result.SHA256, "source_revision", result.SourceRevision, "source_ref", result.SourceRef)
	return result, nil
}

func validateArtifactVerificationInput(options ArtifactVerificationOptions, artifact *os.File) error {
	required := []struct {
		name  string
		value string
	}{
		{name: "repository", value: options.Options.Config.Repo},
		{name: "signer workflow URI", value: options.Options.Config.SignerWorkflowURI},
		{name: "release tag", value: options.ReleaseTag},
		{name: "asset name", value: options.AssetName},
		{name: "SHA-256", value: options.SHA256},
		{name: "source revision", value: options.SourceRevision},
		{name: "source ref", value: options.SourceRef},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	digestBytes, err := hex.DecodeString(options.SHA256)
	if err != nil || len(digestBytes) != sha256.Size {
		return fmt.Errorf("SHA-256 %q must be %d hexadecimal characters", options.SHA256, sha256.Size*2)
	}
	if artifact == nil {
		return fmt.Errorf("artifact file is required")
	}
	info, err := artifact.Stat()
	if err != nil {
		slog.Warn("local artifact stat failed", "path", artifact.Name(), "err", err)
		return fmt.Errorf("stat artifact %s: %w", artifact.Name(), err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("artifact %s is not a regular file", artifact.Name())
	}
	return nil
}

// sha256OpenFile hashes every byte of file with ReadAt and leaves the file
// offset unchanged.
func sha256OpenFile(file *os.File) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, io.NewSectionReader(file, 0, math.MaxInt64)); err != nil {
		slog.Warn("local artifact hash read failed", "path", file.Name(), "err", err)
		return "", fmt.Errorf("hash artifact %s: %w", file.Name(), err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func verifyLocalArtifactChecksum(ctx context.Context, options Options, tagged release, asset releaseAsset, digestHex string) error {
	if checksumFromAsset(asset) == "" {
		// The checksum check downloads checksums.txt to CacheDir when the asset
		// has no digest.
		if err := os.MkdirAll(options.CacheDir, 0o700); err != nil {
			options.Log.WarnContext(ctx, "local artifact cache dir create failed", "path", options.CacheDir, "err", err)
			return fmt.Errorf("create release verification cache dir: %w", err)
		}
	}
	want, err := releaseAssetChecksum(ctx, options, tagged, asset)
	if err != nil {
		return err
	}
	if !strings.EqualFold(want, digestHex) {
		return fmt.Errorf("checksum mismatch for %s: release publishes %s, local artifact has %s", asset.Name, want, digestHex)
	}
	return nil
}
