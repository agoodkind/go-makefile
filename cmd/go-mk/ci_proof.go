package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const ciBuildAudience = "goodkind.io/go-makefile/ci-build"
const githubOIDCIssuer = "https://token.actions.githubusercontent.com"
const githubOIDCJWKSURL = "https://token.actions.githubusercontent.com/.well-known/jwks"
const ciProofClockSkew = 30 * time.Second

// ciProofEnv is the small slice of the GitHub Actions runner environment that
// is required before the engine even tries to request a signed OIDC token.
type ciProofEnv struct {
	githubActions string
	githubRunID   string
	repository    string
	requestURL    string
	requestToken  string
}

// ciProofClient owns network and clock dependencies for OIDC verification.
type ciProofClient struct {
	httpClient *http.Client
	jwksURL    string
	now        func() time.Time
}

type oidcTokenResponse struct {
	Value string `json:"value"`
}

type oidcTokenHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
}

type oidcTokenClaims struct {
	Issuer     string      `json:"iss"`
	Audience   jwtAudience `json:"aud"`
	Repository string      `json:"repository"`
	RunID      string      `json:"run_id"`
	ExpiresAt  int64       `json:"exp"`
	NotBefore  int64       `json:"nbf"`
	IssuedAt   int64       `json:"iat"`
}

type jwtAudience struct {
	values []string
}

type oidcJWKS struct {
	Keys []oidcJWK `json:"keys"`
}

type oidcJWK struct {
	KeyType   string `json:"kty"`
	KeyID     string `json:"kid"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	Modulus   string `json:"n"`
	Exponent  string `json:"e"`
}

func validCIBuildProof() bool {
	return defaultCIProofClient().valid(currentCIProofEnv())
}

func defaultCIProofClient() ciProofClient {
	return ciProofClient{
		httpClient: &http.Client{Timeout: 5 * time.Second},
		jwksURL:    githubOIDCJWKSURL,
		now:        time.Now,
	}
}

func currentCIProofEnv() ciProofEnv {
	return ciProofEnv{
		githubActions: os.Getenv("GITHUB_ACTIONS"),
		githubRunID:   os.Getenv("GITHUB_RUN_ID"),
		repository:    os.Getenv("GITHUB_REPOSITORY"),
		requestURL:    os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"),
		requestToken:  os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN"),
	}
}

func (env ciProofEnv) complete() bool {
	if env.githubActions != "true" {
		return false
	}
	if strings.TrimSpace(env.githubRunID) == "" {
		return false
	}
	if strings.TrimSpace(env.repository) == "" {
		return false
	}
	if strings.TrimSpace(env.requestURL) == "" {
		return false
	}
	if strings.TrimSpace(env.requestToken) == "" {
		return false
	}
	return true
}

func (client ciProofClient) valid(env ciProofEnv) bool {
	if !env.complete() {
		return false
	}
	token, ok := client.requestOIDCToken(env)
	if !ok {
		return false
	}
	jwks, ok := client.fetchJWKS()
	if !ok {
		return false
	}
	return client.verifyToken(token, jwks, env)
}

func (client ciProofClient) requestOIDCToken(env ciProofEnv) (string, bool) {
	requestURL, ok := tokenRequestURL(env.requestURL)
	if !ok {
		return "", false
	}
	slog.Info("ci proof request oidc token")
	request, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return "", false
	}
	request.Header.Set("Authorization", "Bearer "+env.requestToken)
	request.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(request)
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
	var tokenResponse oidcTokenResponse
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		return "", false
	}
	if strings.TrimSpace(tokenResponse.Value) == "" {
		return "", false
	}
	return tokenResponse.Value, true
}

func tokenRequestURL(rawURL string) (string, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	query := parsed.Query()
	query.Set("audience", ciBuildAudience)
	parsed.RawQuery = query.Encode()
	return parsed.String(), true
}

func (client ciProofClient) fetchJWKS() (oidcJWKS, bool) {
	slog.Info("ci proof fetch jwks")
	response, err := client.httpClient.Get(client.jwksURL)
	if err != nil {
		return oidcJWKS{}, false
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return oidcJWKS{}, false
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return oidcJWKS{}, false
	}
	var jwks oidcJWKS
	if err := json.Unmarshal(body, &jwks); err != nil {
		return oidcJWKS{}, false
	}
	if len(jwks.Keys) == 0 {
		return oidcJWKS{}, false
	}
	return jwks, true
}

func (client ciProofClient) verifyToken(token string, jwks oidcJWKS, env ciProofEnv) bool {
	header, claims, signedPart, signature, ok := parseOIDCToken(token)
	if !ok {
		return false
	}
	if header.Algorithm != "RS256" {
		return false
	}
	if strings.TrimSpace(header.KeyID) == "" {
		return false
	}
	key, ok := jwks.rsaKey(header.KeyID)
	if !ok {
		return false
	}
	if !verifyOIDCSignature(signedPart, signature, key) {
		return false
	}
	return client.claimsMatch(claims, env)
}

func parseOIDCToken(token string) (oidcTokenHeader, oidcTokenClaims, string, []byte, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return oidcTokenHeader{}, oidcTokenClaims{}, "", nil, false
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return oidcTokenHeader{}, oidcTokenClaims{}, "", nil, false
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return oidcTokenHeader{}, oidcTokenClaims{}, "", nil, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return oidcTokenHeader{}, oidcTokenClaims{}, "", nil, false
	}
	var header oidcTokenHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return oidcTokenHeader{}, oidcTokenClaims{}, "", nil, false
	}
	var claims oidcTokenClaims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return oidcTokenHeader{}, oidcTokenClaims{}, "", nil, false
	}
	return header, claims, parts[0] + "." + parts[1], signature, true
}

func (jwks oidcJWKS) rsaKey(keyID string) (*rsa.PublicKey, bool) {
	for _, key := range jwks.Keys {
		if key.KeyID != keyID {
			continue
		}
		if !key.usableRSAKey() {
			return nil, false
		}
		return key.publicKey()
	}
	return nil, false
}

func (key oidcJWK) usableRSAKey() bool {
	if key.KeyType != "RSA" {
		return false
	}
	if key.Use != "" && key.Use != "sig" {
		return false
	}
	if key.Algorithm != "" && key.Algorithm != "RS256" {
		return false
	}
	return true
}

func (key oidcJWK) publicKey() (*rsa.PublicKey, bool) {
	modulusBytes, err := base64.RawURLEncoding.DecodeString(key.Modulus)
	if err != nil {
		return nil, false
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(key.Exponent)
	if err != nil {
		return nil, false
	}
	modulus := new(big.Int).SetBytes(modulusBytes)
	exponent := new(big.Int).SetBytes(exponentBytes)
	if modulus.Sign() <= 0 {
		return nil, false
	}
	if !exponent.IsInt64() {
		return nil, false
	}
	exponentValue := exponent.Int64()
	if exponentValue <= 0 || exponentValue > 1<<31-1 {
		return nil, false
	}
	return &rsa.PublicKey{N: modulus, E: int(exponentValue)}, true
}

func verifyOIDCSignature(signedPart string, signature []byte, key *rsa.PublicKey) bool {
	digest := sha256.Sum256([]byte(signedPart))
	err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature)
	return err == nil
}

func (client ciProofClient) claimsMatch(claims oidcTokenClaims, env ciProofEnv) bool {
	if claims.Issuer != githubOIDCIssuer {
		return false
	}
	if !claims.Audience.Contains(ciBuildAudience) {
		return false
	}
	if claims.Repository != env.repository {
		return false
	}
	if claims.RunID != env.githubRunID {
		return false
	}
	now := client.now()
	if claims.ExpiresAt <= now.Add(-ciProofClockSkew).Unix() {
		return false
	}
	if claims.NotBefore != 0 && claims.NotBefore > now.Add(ciProofClockSkew).Unix() {
		return false
	}
	if claims.IssuedAt != 0 && claims.IssuedAt > now.Add(ciProofClockSkew).Unix() {
		return false
	}
	return true
}

func (audience *jwtAudience) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		audience.values = []string{single}
		return nil
	}
	var values []string
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	audience.values = values
	return nil
}

func (audience jwtAudience) Contains(expected string) bool {
	for _, value := range audience.values {
		if value == expected {
			return true
		}
	}
	return false
}
