package main

import "testing"

func TestIsStableRef(t *testing.T) {
	cases := []struct {
		name    string
		gitRef  string
		refName string
		want    bool
	}{
		{name: "semver tag is stable", gitRef: "refs/tags/v0.1.0", refName: "v0.1.0", want: true},
		{name: "v-prefixed tag is stable", gitRef: "refs/tags/v1", refName: "v1", want: true},
		{name: "main branch is prerelease", gitRef: "refs/heads/main", refName: "main", want: false},
		{name: "non-v tag is prerelease", gitRef: "refs/tags/2026.06.03", refName: "2026.06.03", want: false},
		{name: "empty ref is prerelease", gitRef: "", refName: "", want: false},
		{name: "v branch name without tag ref is prerelease", gitRef: "refs/heads/victory", refName: "victory", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := isStableRef(testCase.gitRef, testCase.refName)
			if got != testCase.want {
				t.Fatalf("isStableRef(%q, %q) = %v, want %v", testCase.gitRef, testCase.refName, got, testCase.want)
			}
		})
	}
}

func TestEnvTruthy(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "one", value: "1", want: true},
		{name: "true", value: "true", want: true},
		{name: "yes", value: "yes", want: true},
		{name: "on", value: "on", want: true},
		{name: "empty", value: "", want: false},
		{name: "zero", value: "0", want: false},
		{name: "false", value: "false", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := envTruthy(testCase.value); got != testCase.want {
				t.Fatalf("envTruthy(%q) = %v, want %v", testCase.value, got, testCase.want)
			}
		})
	}
}

func TestSignDarwinBinariesRequiresSigningWhenConfigured(t *testing.T) {
	t.Setenv("QUILL_SIGN_P12", "")
	err := signDarwinBinaries(releaseConfig{
		requireDarwinCodesign: true,
		platforms:             []string{"darwin/arm64"},
	})
	if err == nil {
		t.Fatal("signDarwinBinaries() = nil, want error")
	}
	if err.Error() != "release: darwin signing required but QUILL_SIGN_P12 is unset" {
		t.Fatalf("signDarwinBinaries() error = %v", err)
	}
}

func TestSignDarwinBinariesSkipsWhenNoDarwinTargets(t *testing.T) {
	t.Setenv("QUILL_SIGN_P12", "")
	if err := signDarwinBinaries(releaseConfig{
		requireDarwinCodesign: true,
		platforms:             []string{"linux/amd64"},
	}); err != nil {
		t.Fatalf("signDarwinBinaries() error = %v, want nil", err)
	}
}
