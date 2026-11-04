package selfupdate

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"

	in_toto "github.com/in-toto/attestation/go/v1"
	sigbundle "github.com/sigstore/sigstore-go/pkg/bundle"
	fulciocert "github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	sigroot "github.com/sigstore/sigstore-go/pkg/root"
	sigverify "github.com/sigstore/sigstore-go/pkg/verify"
)

const (
	githubActionsOIDCIssuer            = "https://token.actions.githubusercontent.com"
	githubBuildProvenancePredicateType = "https://slsa.dev/provenance/v1"
	githubHostedRunnerEnvironment      = "github-hosted"
	// goMakefileWorkflowPattern accepts any go-makefile workflow file on
	// main. A deployed binary keeps verifying releases after go-makefile
	// renames its packaging workflow.
	goMakefileWorkflowPattern = `^https://github\.com/agoodkind/go-makefile/\.github/workflows/[A-Za-z0-9_.-]+\.ya?ml@refs/heads/main$`
	// provenanceBundleSuffix follows the archive name in the name of the
	// release asset that stores the Sigstore bundle for that archive.
	provenanceBundleSuffix = ".sigstore.json"
	maxProvenanceBundle    = 16 << 20
	minShortCommitLength   = 7
	tagRefPrefix           = "refs/tags/"
)

var goMakefileWorkflow = regexp.MustCompile(goMakefileWorkflowPattern)

// verifyGitHubAttestations downloads the Sigstore bundle published beside the
// archive and verifies that the bundle binds the archive digest to the
// go-makefile release workflow and to the commit that the release tag records.
func verifyGitHubAttestations(ctx context.Context, options Options, latest release, asset releaseAsset, archivePath string) error {
	digestHex, err := sha256File(archivePath)
	if err != nil {
		return err
	}
	digestBytes, err := hex.DecodeString(digestHex)
	if err != nil {
		return fmt.Errorf("decode artifact digest: %w", err)
	}
	bundleJSON, err := downloadProvenanceBundle(ctx, options, latest, asset, archivePath)
	if err != nil {
		options.Log.WarnContext(ctx, "update build provenance bundle download failed", "asset", asset.Name, "tag", latest.TagName, "err", err)
		return err
	}
	if err := verifyBuildProvenanceBundle(bundleJSON, options, latest.TagName, asset.Name, digestHex, digestBytes); err != nil {
		options.Log.WarnContext(ctx, "update build provenance verification failed", "asset", asset.Name, "repo", options.Config.Repo, "err", err)
		return err
	}
	return nil
}

func downloadProvenanceBundle(ctx context.Context, options Options, latest release, asset releaseAsset, archivePath string) (json.RawMessage, error) {
	bundleName := asset.Name + provenanceBundleSuffix
	bundlePath := archivePath + provenanceBundleSuffix
	bundleURL := releaseAssetURL(options.Config, latest.TagName, bundleName)
	if err := updateDownloadFile(ctx, options.Client, bundleURL, bundlePath, maxProvenanceBundle); err != nil {
		slog.WarnContext(ctx, "update build provenance bundle request failed", "asset", bundleName, "err", err)
		return nil, fmt.Errorf("download build provenance bundle %s: %w", bundleName, err)
	}
	bundleJSON, err := os.ReadFile(bundlePath)
	if err != nil {
		slog.WarnContext(ctx, "update build provenance bundle read failed", "asset", bundleName, "err", err)
		return nil, fmt.Errorf("read build provenance bundle %s: %w", bundleName, err)
	}
	return bundleJSON, nil
}

func verifyBuildProvenanceBundle(bundleJSON json.RawMessage, options Options, tag string, assetName string, digestHex string, digestBytes []byte) error {
	repo := options.Config.Repo
	sourceRepositoryURI := githubRepositoryURI(repo)
	signerWorkflowPattern := options.Config.signerWorkflowPattern()
	sanMatcher, err := sigverify.NewSANMatcher("", signerWorkflowPattern.String())
	if err != nil {
		return fmt.Errorf("build provenance SAN matcher: %w", err)
	}
	issuerMatcher, err := sigverify.NewIssuerMatcher(githubActionsOIDCIssuer, "")
	if err != nil {
		return fmt.Errorf("build provenance issuer matcher: %w", err)
	}
	// Fulcio extensions support only exact values, so
	// validateBuildProvenanceCertificate checks BuildSignerURI against the
	// verified SAN after verification.
	identity, err := sigverify.NewCertificateIdentity(sanMatcher, issuerMatcher, fulciocert.Extensions{
		RunnerEnvironment:   githubHostedRunnerEnvironment,
		SourceRepositoryURI: sourceRepositoryURI,
	})
	if err != nil {
		return fmt.Errorf("build provenance certificate identity: %w", err)
	}
	verifier, err := newBuildProvenanceVerifier()
	if err != nil {
		return err
	}
	policy := sigverify.NewPolicy(
		sigverify.WithArtifactDigest("sha256", digestBytes),
		sigverify.WithCertificateIdentity(identity),
	)
	result, err := verifyBundle(bundleJSON, verifier, policy)
	if err == nil {
		err = validateBuildProvenance(result, repo, assetName, digestHex, signerWorkflowPattern)
	}
	if err == nil {
		err = validateReleaseTagBinding(result.Signature.Certificate, tag)
	}
	if err != nil {
		slog.Warn("update build provenance bundle rejected", "asset", assetName, "tag", tag, "err", err)
		return fmt.Errorf("build provenance verification failed: %w", err)
	}
	return nil
}

func newBuildProvenanceVerifier() (*sigverify.Verifier, error) {
	trustedRoot, err := sigroot.FetchTrustedRoot()
	if err != nil {
		slog.Warn("update sigstore trusted root fetch failed", "err", err)
		return nil, fmt.Errorf("fetch sigstore trusted root: %w", err)
	}
	verifier, err := sigverify.NewVerifier(
		trustedRoot,
		sigverify.WithIntegratedTimestamps(1),
		sigverify.WithTransparencyLog(1),
	)
	if err != nil {
		slog.Warn("update sigstore verifier create failed", "err", err)
		return nil, fmt.Errorf("create sigstore verifier: %w", err)
	}
	return verifier, nil
}

func verifyBundle(bundleJSON json.RawMessage, verifier *sigverify.Verifier, policy sigverify.PolicyBuilder) (*sigverify.VerificationResult, error) {
	var attestationBundle sigbundle.Bundle
	if err := json.Unmarshal(bundleJSON, &attestationBundle); err != nil {
		slog.Warn("update attestation bundle decode failed", "err", err)
		return nil, fmt.Errorf("decode attestation bundle: %w", err)
	}
	result, err := verifier.Verify(&attestationBundle, policy)
	if err != nil {
		slog.Warn("update attestation bundle verify failed", "err", err)
		return nil, fmt.Errorf("verify attestation bundle: %w", err)
	}
	return result, nil
}

func validateBuildProvenance(result *sigverify.VerificationResult, repo string, assetName string, digestHex string, signerWorkflowPattern *regexp.Regexp) error {
	if result.Statement == nil {
		return fmt.Errorf("build provenance statement missing")
	}
	if result.Statement.GetPredicateType() != githubBuildProvenancePredicateType {
		return fmt.Errorf("build provenance predicate type %q did not match %q", result.Statement.GetPredicateType(), githubBuildProvenancePredicateType)
	}
	if !statementHasSHA256SubjectDigest(result.Statement.GetSubject(), assetName, digestHex) {
		return fmt.Errorf("build provenance did not include %s with digest %s", assetName, digestHex)
	}
	if result.Signature == nil || result.Signature.Certificate == nil {
		return fmt.Errorf("build provenance certificate summary missing")
	}
	return validateBuildProvenanceCertificate(result.Signature.Certificate, repo, signerWorkflowPattern)
}

func validateBuildProvenanceCertificate(summary *fulciocert.Summary, repo string, signerWorkflowPattern *regexp.Regexp) error {
	if !signerWorkflowPattern.MatchString(summary.SubjectAlternativeName) {
		return fmt.Errorf("build provenance SAN %q did not match %q", summary.SubjectAlternativeName, signerWorkflowPattern.String())
	}
	if summary.Issuer != githubActionsOIDCIssuer {
		return fmt.Errorf("build provenance issuer %q did not match %q", summary.Issuer, githubActionsOIDCIssuer)
	}
	if summary.BuildSignerURI != summary.SubjectAlternativeName {
		return fmt.Errorf("build signer URI %q did not match SAN %q", summary.BuildSignerURI, summary.SubjectAlternativeName)
	}
	if summary.RunnerEnvironment != githubHostedRunnerEnvironment {
		return fmt.Errorf("runner environment %q did not match %q", summary.RunnerEnvironment, githubHostedRunnerEnvironment)
	}
	sourceRepositoryURI := githubRepositoryURI(repo)
	if summary.SourceRepositoryURI != sourceRepositoryURI {
		return fmt.Errorf("source repository URI %q did not match %q", summary.SourceRepositoryURI, sourceRepositoryURI)
	}
	return nil
}

// validateReleaseTagBinding requires the verified certificate to match the
// release tag. A rolling tag ends in a short commit, and the certificate source
// commit must start with it. A stable v-tag has no commit suffix. The release
// workflow runs on that tag, and the certificate source ref must equal it.
func validateReleaseTagBinding(summary *fulciocert.Summary, tag string) error {
	if shortCommit, found := tagShortCommit(tag); found {
		if !strings.HasPrefix(summary.SourceRepositoryDigest, shortCommit) {
			return fmt.Errorf("source commit %s does not start with tag short commit %s", summary.SourceRepositoryDigest, shortCommit)
		}
		return nil
	}
	wantRef := tagRefPrefix + tag
	if summary.SourceRepositoryRef != wantRef {
		return fmt.Errorf("source ref %q did not match %q", summary.SourceRepositoryRef, wantRef)
	}
	return nil
}

func tagShortCommit(tag string) (string, bool) {
	separator := strings.LastIndex(tag, "-")
	if separator < 0 {
		return "", false
	}
	suffix := tag[separator+1:]
	if len(suffix) < minShortCommitLength {
		return "", false
	}
	for _, character := range suffix {
		isDigit := character >= '0' && character <= '9'
		isLowerHexLetter := character >= 'a' && character <= 'f'
		if !isDigit && !isLowerHexLetter {
			return "", false
		}
	}
	return suffix, true
}

func githubRepositoryURI(repo string) string {
	return "https://github.com/" + repo
}

func statementHasSHA256SubjectDigest(subjects []*in_toto.ResourceDescriptor, name string, digestHex string) bool {
	for _, subject := range subjects {
		if subject.GetName() != name {
			continue
		}
		if subject.GetDigest()["sha256"] == digestHex {
			return true
		}
	}
	return false
}
