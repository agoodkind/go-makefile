package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testKeyID = "test-key"

type testJWTHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

type testOIDCClaims struct {
	Issuer     string `json:"iss"`
	Audience   string `json:"aud"`
	Repository string `json:"repository"`
	RunID      string `json:"run_id"`
	ExpiresAt  int64  `json:"exp"`
	NotBefore  int64  `json:"nbf"`
	IssuedAt   int64  `json:"iat"`
}

func TestCIBuildProofValidSignedToken(t *testing.T) {
	now := time.Date(2026, time.June, 16, 12, 0, 0, 0, time.UTC)
	key := testRSAKey(t)
	token := signedTestOIDCToken(t, key, testKeyID, defaultTestClaims(now))
	server := testOIDCServer(t, token, oidcJWKS{Keys: []oidcJWK{testJWK(key, testKeyID)}})
	defer server.Close()

	client := testCIProofClient(server, now)
	if !client.valid(testCIProofEnv(server)) {
		t.Fatal("valid signed GitHub OIDC proof was rejected")
	}
}

func TestCIBuildProofMissingOIDCEnvKeepsInlineGate(t *testing.T) {
	now := time.Date(2026, time.June, 16, 12, 0, 0, 0, time.UTC)
	client := ciProofClient{
		httpClient: &http.Client{Transport: failingRoundTripper{t: t}},
		jwksURL:    "https://example.invalid/jwks",
		now: func() time.Time {
			return now
		},
	}
	if client.valid(ciProofEnv{}) {
		t.Fatal("missing OIDC request env was accepted")
	}
}

func TestCIBuildProofRejectsSpoofedActionsEnv(t *testing.T) {
	now := time.Date(2026, time.June, 16, 12, 0, 0, 0, time.UTC)
	client := ciProofClient{
		httpClient: &http.Client{Transport: failingRoundTripper{t: t}},
		jwksURL:    "https://example.invalid/jwks",
		now: func() time.Time {
			return now
		},
	}
	env := ciProofEnv{
		githubActions: "true",
		githubRunID:   "123456",
		repository:    "agoodkind/example",
		requestURL:    "",
		requestToken:  "",
	}
	if client.valid(env) {
		t.Fatal("spoofed GitHub Actions env without OIDC proof was accepted")
	}
}

func TestCIBuildProofRejectsInvalidClaimsAndSignatures(t *testing.T) {
	now := time.Date(2026, time.June, 16, 12, 0, 0, 0, time.UTC)
	testCases := []struct {
		name      string
		claims    testOIDCClaims
		verifyKey func(t *testing.T, signingKey *rsa.PrivateKey) *rsa.PrivateKey
	}{
		{
			name:      "wrong audience",
			claims:    withAudience(defaultTestClaims(now), "wrong-audience"),
			verifyKey: sameRSAKey,
		},
		{
			name:      "wrong repository",
			claims:    withRepository(defaultTestClaims(now), "agoodkind/other"),
			verifyKey: sameRSAKey,
		},
		{
			name:      "wrong run id",
			claims:    withRunID(defaultTestClaims(now), "654321"),
			verifyKey: sameRSAKey,
		},
		{
			name:      "expired token",
			claims:    withExpiresAt(defaultTestClaims(now), now.Add(-time.Hour).Unix()),
			verifyKey: sameRSAKey,
		},
		{
			name:      "bad signature",
			claims:    defaultTestClaims(now),
			verifyKey: differentRSAKey,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			signingKey := testRSAKey(t)
			token := signedTestOIDCToken(t, signingKey, testKeyID, testCase.claims)
			verifyKey := testCase.verifyKey(t, signingKey)
			jwks := oidcJWKS{Keys: []oidcJWK{testJWK(verifyKey, testKeyID)}}
			server := testOIDCServer(t, token, jwks)
			defer server.Close()

			client := testCIProofClient(server, now)
			if client.valid(testCIProofEnv(server)) {
				t.Fatalf("%s was accepted", testCase.name)
			}
		})
	}
}

type failingRoundTripper struct {
	t *testing.T
}

func (transport failingRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	transport.t.Fatal("unexpected network request")
	return nil, errors.New("unexpected network request")
}

func testCIProofEnv(server *httptest.Server) ciProofEnv {
	return ciProofEnv{
		githubActions: "true",
		githubRunID:   "123456",
		repository:    "agoodkind/example",
		requestURL:    server.URL + "/token",
		requestToken:  "request-token",
	}
}

func testCIProofClient(server *httptest.Server, now time.Time) ciProofClient {
	return ciProofClient{
		httpClient: server.Client(),
		jwksURL:    server.URL + "/jwks",
		now: func() time.Time {
			return now
		},
	}
}

func testOIDCServer(t *testing.T, token string, jwks oidcJWKS) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/token" {
			if request.Header.Get("Authorization") != "Bearer request-token" {
				t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
			}
			if request.URL.Query().Get("audience") != ciBuildAudience {
				t.Fatalf("audience = %q", request.URL.Query().Get("audience"))
			}
			if err := json.NewEncoder(writer).Encode(oidcTokenResponse{Value: token}); err != nil {
				t.Fatalf("write token response: %v", err)
			}
			return
		}
		if request.URL.Path == "/jwks" {
			if err := json.NewEncoder(writer).Encode(jwks); err != nil {
				t.Fatalf("write jwks response: %v", err)
			}
			return
		}
		http.NotFound(writer, request)
	}))
}

func defaultTestClaims(now time.Time) testOIDCClaims {
	return testOIDCClaims{
		Issuer:     githubOIDCIssuer,
		Audience:   ciBuildAudience,
		Repository: "agoodkind/example",
		RunID:      "123456",
		ExpiresAt:  now.Add(time.Hour).Unix(),
		NotBefore:  now.Add(-time.Minute).Unix(),
		IssuedAt:   now.Add(-time.Minute).Unix(),
	}
}

func signedTestOIDCToken(
	t *testing.T,
	key *rsa.PrivateKey,
	keyID string,
	claims testOIDCClaims,
) string {
	t.Helper()
	header := testJWTHeader{Algorithm: "RS256", KeyID: keyID, Type: "JWT"}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	headerPart := base64.RawURLEncoding.EncodeToString(headerBytes)
	claimsPart := base64.RawURLEncoding.EncodeToString(claimsBytes)
	signedPart := headerPart + "." + claimsPart
	digest := sha256.Sum256([]byte(signedPart))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	signaturePart := base64.RawURLEncoding.EncodeToString(signature)
	return strings.Join([]string{headerPart, claimsPart, signaturePart}, ".")
}

func testJWK(key *rsa.PrivateKey, keyID string) oidcJWK {
	exponent := big.NewInt(int64(key.PublicKey.E)).Bytes()
	return oidcJWK{
		KeyType:   "RSA",
		KeyID:     keyID,
		Use:       "sig",
		Algorithm: "RS256",
		Modulus:   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
		Exponent:  base64.RawURLEncoding.EncodeToString(exponent),
	}
}

func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func sameRSAKey(_ *testing.T, signingKey *rsa.PrivateKey) *rsa.PrivateKey {
	return signingKey
}

func differentRSAKey(t *testing.T, _ *rsa.PrivateKey) *rsa.PrivateKey {
	t.Helper()
	return testRSAKey(t)
}

func withAudience(claims testOIDCClaims, audience string) testOIDCClaims {
	claims.Audience = audience
	return claims
}

func withRepository(claims testOIDCClaims, repository string) testOIDCClaims {
	claims.Repository = repository
	return claims
}

func withRunID(claims testOIDCClaims, runID string) testOIDCClaims {
	claims.RunID = runID
	return claims
}

func withExpiresAt(claims testOIDCClaims, expiresAt int64) testOIDCClaims {
	claims.ExpiresAt = expiresAt
	return claims
}
