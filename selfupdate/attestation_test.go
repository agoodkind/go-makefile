package selfupdate

import (
	"strings"
	"testing"

	in_toto "github.com/in-toto/attestation/go/v1"
	fulciocert "github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	sigverify "github.com/sigstore/sigstore-go/pkg/verify"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestValidateReleaseAttestation(t *testing.T) {
	predicate, err := structpb.NewStruct(map[string]any{
		"repository": "agoodkind/agent-gate",
		"tag":        "v1.2.3",
	})
	if err != nil {
		t.Fatalf("NewStruct() error: %v", err)
	}
	result := &sigverify.VerificationResult{
		Statement: &in_toto.Statement{
			PredicateType: githubReleaseAttestationPredicateType,
			Predicate:     predicate,
			Subject: []*in_toto.ResourceDescriptor{
				{Name: "agent-gate_darwin_arm64.tar.gz", Digest: map[string]string{"sha256": "deadbeef"}},
			},
		},
	}
	if err := validateReleaseAttestation(result, "agoodkind/agent-gate", "v1.2.3", "agent-gate_darwin_arm64.tar.gz", "deadbeef"); err != nil {
		t.Fatalf("validateReleaseAttestation() error: %v", err)
	}
}

func TestValidateReleaseAttestationRejectsMismatches(t *testing.T) {
	predicate, err := structpb.NewStruct(map[string]any{
		"repository": "agoodkind/agent-gate",
		"tag":        "v1.2.3",
	})
	if err != nil {
		t.Fatalf("NewStruct() error: %v", err)
	}
	baseResult := &sigverify.VerificationResult{
		Statement: &in_toto.Statement{
			PredicateType: githubReleaseAttestationPredicateType,
			Predicate:     predicate,
			Subject: []*in_toto.ResourceDescriptor{
				{Name: "agent-gate_darwin_arm64.tar.gz", Digest: map[string]string{"sha256": "deadbeef"}},
			},
		},
	}
	testCases := []struct {
		name      string
		repo      string
		tag       string
		assetName string
		digestHex string
		want      string
	}{
		{
			name:      "wrong repo",
			repo:      "agoodkind/not-agent-gate",
			tag:       "v1.2.3",
			assetName: "agent-gate_darwin_arm64.tar.gz",
			digestHex: "deadbeef",
			want:      "did not match",
		},
		{
			name:      "wrong tag",
			repo:      "agoodkind/agent-gate",
			tag:       "v9.9.9",
			assetName: "agent-gate_darwin_arm64.tar.gz",
			digestHex: "deadbeef",
			want:      "did not match",
		},
		{
			name:      "missing subject digest",
			repo:      "agoodkind/agent-gate",
			tag:       "v1.2.3",
			assetName: "agent-gate_linux_arm64.tar.gz",
			digestHex: "deadbeef",
			want:      "did not include",
		},
		{
			name:      "wrong subject digest",
			repo:      "agoodkind/agent-gate",
			tag:       "v1.2.3",
			assetName: "agent-gate_darwin_arm64.tar.gz",
			digestHex: "cafebabe",
			want:      "did not include",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateReleaseAttestation(baseResult, testCase.repo, testCase.tag, testCase.assetName, testCase.digestHex)
			if err == nil {
				t.Fatal("validateReleaseAttestation() error = nil, want mismatch")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("validateReleaseAttestation() error = %v, want substring %q", err, testCase.want)
			}
		})
	}
}

const (
	testPackageWorkflowURI      = "https://github.com/agoodkind/go-makefile/.github/workflows/_package.yml@refs/heads/main"
	testReleaseBuildWorkflowURI = "https://github.com/agoodkind/go-makefile/.github/workflows/_release_build.yml@refs/heads/main"
)

func buildProvenanceSummary(signerWorkflowURI string, repo string) *fulciocert.Summary {
	return &fulciocert.Summary{
		SubjectAlternativeName: signerWorkflowURI,
		Extensions: fulciocert.Extensions{
			Issuer:              githubActionsOIDCIssuer,
			BuildSignerURI:      signerWorkflowURI,
			RunnerEnvironment:   githubHostedRunnerEnvironment,
			SourceRepositoryURI: githubRepositoryURI(repo),
		},
	}
}

// The default pattern accepts both the old and the renamed packaging
// workflow. A binary built before a workflow rename must accept releases
// signed by the renamed workflow.
func TestValidateBuildProvenanceCertificateAcceptsAnyGoMakefileWorkflowOnMain(t *testing.T) {
	pattern := Config{}.signerWorkflowPattern()
	for _, signerWorkflowURI := range []string{testPackageWorkflowURI, testReleaseBuildWorkflowURI} {
		summary := buildProvenanceSummary(signerWorkflowURI, "agoodkind/agent-gate")
		if err := validateBuildProvenanceCertificate(summary, "agoodkind/agent-gate", pattern); err != nil {
			t.Fatalf("validateBuildProvenanceCertificate(%s) error: %v", signerWorkflowURI, err)
		}
	}
}

func TestValidateBuildProvenanceCertificateExactOverride(t *testing.T) {
	pattern := Config{SignerWorkflowURI: testPackageWorkflowURI}.signerWorkflowPattern()
	if err := validateBuildProvenanceCertificate(buildProvenanceSummary(testPackageWorkflowURI, "agoodkind/agent-gate"), "agoodkind/agent-gate", pattern); err != nil {
		t.Fatalf("validateBuildProvenanceCertificate() error: %v", err)
	}
	err := validateBuildProvenanceCertificate(buildProvenanceSummary(testReleaseBuildWorkflowURI, "agoodkind/agent-gate"), "agoodkind/agent-gate", pattern)
	if err == nil || !strings.Contains(err.Error(), "SAN") {
		t.Fatalf("validateBuildProvenanceCertificate() error = %v, want SAN mismatch for another workflow", err)
	}
}

func TestValidateBuildProvenanceCertificateRejectsMismatches(t *testing.T) {
	testCases := []struct {
		name    string
		summary *fulciocert.Summary
		repo    string
		want    string
	}{
		{
			name:    "workflow outside go-makefile",
			summary: buildProvenanceSummary("https://github.com/agoodkind/agent-gate/.github/workflows/_package.yml@refs/heads/main", "agoodkind/agent-gate"),
			repo:    "agoodkind/agent-gate",
			want:    "SAN",
		},
		{
			name:    "go-makefile workflow on another ref",
			summary: buildProvenanceSummary("https://github.com/agoodkind/go-makefile/.github/workflows/_package.yml@refs/heads/feature", "agoodkind/agent-gate"),
			repo:    "agoodkind/agent-gate",
			want:    "SAN",
		},
		{
			name: "build signer differs from SAN",
			summary: &fulciocert.Summary{
				SubjectAlternativeName: testPackageWorkflowURI,
				Extensions: fulciocert.Extensions{
					Issuer:              githubActionsOIDCIssuer,
					BuildSignerURI:      testReleaseBuildWorkflowURI,
					RunnerEnvironment:   githubHostedRunnerEnvironment,
					SourceRepositoryURI: githubRepositoryURI("agoodkind/agent-gate"),
				},
			},
			repo: "agoodkind/agent-gate",
			want: "build signer URI",
		},
		{
			name: "wrong repo",
			summary: buildProvenanceSummary(testPackageWorkflowURI, "agoodkind/go-makefile"),
			repo:    "agoodkind/agent-gate",
			want:    "source repository URI",
		},
		{
			name: "wrong issuer",
			summary: &fulciocert.Summary{
				SubjectAlternativeName: testPackageWorkflowURI,
				Extensions: fulciocert.Extensions{
					Issuer:              "https://issuer.example.invalid",
					BuildSignerURI:      testPackageWorkflowURI,
					RunnerEnvironment:   githubHostedRunnerEnvironment,
					SourceRepositoryURI: githubRepositoryURI("agoodkind/agent-gate"),
				},
			},
			repo: "agoodkind/agent-gate",
			want: "issuer",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateBuildProvenanceCertificate(testCase.summary, testCase.repo, Config{}.signerWorkflowPattern())
			if err == nil {
				t.Fatal("validateBuildProvenanceCertificate() error = nil, want mismatch")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("validateBuildProvenanceCertificate() error = %v, want substring %q", err, testCase.want)
			}
		})
	}
}

func TestValidateBuildProvenanceRejectsMissingCertificateSummary(t *testing.T) {
	result := &sigverify.VerificationResult{
		Statement: &in_toto.Statement{
			PredicateType: githubBuildProvenancePredicateType,
			Subject: []*in_toto.ResourceDescriptor{
				{Name: "agent-gate_darwin_arm64.tar.gz", Digest: map[string]string{"sha256": "deadbeef"}},
			},
		},
	}
	err := validateBuildProvenance(
		result,
		"agoodkind/agent-gate",
		"agent-gate_darwin_arm64.tar.gz",
		"deadbeef",
		Config{}.signerWorkflowPattern(),
	)
	if err == nil {
		t.Fatal("validateBuildProvenance() error = nil, want missing certificate summary")
	}
	if !strings.Contains(err.Error(), "certificate summary missing") {
		t.Fatalf("validateBuildProvenance() error = %v", err)
	}
}

func TestSplitRepositoryRejectsExtraSegments(t *testing.T) {
	_, _, err := splitRepository("agoodkind/agent-gate/extra")
	if err == nil {
		t.Fatal("splitRepository() error = nil, want extra segment rejection")
	}
	if !strings.Contains(err.Error(), "owner/name") {
		t.Fatalf("splitRepository() error = %v", err)
	}
}
