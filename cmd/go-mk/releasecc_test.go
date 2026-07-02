package main

import (
	"slices"
	"testing"
)

func TestCrossCompilerEnv(t *testing.T) {
	cases := []struct {
		name string
		cc   string
		cxx  string
		want []string
	}{
		{name: "both set (darwin cross)", cc: "oa64-clang", cxx: "oa64-clang++", want: []string{"CC=oa64-clang", "CXX=oa64-clang++"}},
		{name: "ccache wrapped darwin cross", cc: "ccache oa64-clang", cxx: "ccache oa64-clang++", want: []string{"CC=ccache oa64-clang", "CXX=ccache oa64-clang++"}},
		{name: "unset (native build)", cc: "", cxx: "", want: []string{}},
		{name: "blank is treated as unset", cc: "  ", cxx: "  ", want: []string{}},
		{name: "cc only", cc: "o64-clang", cxx: "", want: []string{"CC=o64-clang"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("GO_MK_CC", testCase.cc)
			t.Setenv("GO_MK_CXX", testCase.cxx)
			if got := crossCompilerEnv(); !slices.Equal(got, testCase.want) {
				t.Fatalf("crossCompilerEnv() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestBuildPlatformEnvAppliesCrossCompiler(t *testing.T) {
	t.Setenv("GO_MK_CC", "ccache oa64-clang")
	t.Setenv("GO_MK_CXX", "ccache oa64-clang++")
	env := buildPlatformEnv("darwin", "arm64", "", "")
	if !slices.Contains(env, "CC=ccache oa64-clang") || !slices.Contains(env, "CXX=ccache oa64-clang++") {
		t.Fatalf("darwin build env should carry the cross compiler, got %v", env)
	}
}

func TestBuildPlatformEnvNativeHasNoCompilerOverride(t *testing.T) {
	t.Setenv("GO_MK_CC", "")
	t.Setenv("GO_MK_CXX", "")
	env := buildPlatformEnv("linux", "amd64", "", "")
	for _, entry := range env {
		if len(entry) >= 3 && entry[:3] == "CC=" {
			t.Fatalf("native build env must not override CC (so ccache CC survives), got %v", env)
		}
	}
}
