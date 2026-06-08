package findings

import "testing"

func TestFindingPath(t *testing.T) {
	testCases := []struct {
		name     string
		line     string
		wantPath string
		wantOK   bool
	}{
		{
			name:     "relative path with location",
			line:     "cmd/main.go:6:11: Error return value is not checked (errcheck)",
			wantPath: "cmd/main.go",
			wantOK:   true,
		},
		{
			name:     "absolute path with location",
			line:     "/abs/pkg/file.go:1:2: something (linter)",
			wantPath: "/abs/pkg/file.go",
			wantOK:   true,
		},
		{
			name:     "dotdot escaping path with location",
			line:     "../../phase1-shelldecomp/api/daemonpb/daemon.pb.go:7:9: package should have a godoc (godoclint)",
			wantPath: "../../phase1-shelldecomp/api/daemonpb/daemon.pb.go",
			wantOK:   true,
		},
		{
			name:     "no location run",
			line:     "a trailing linter tag with no location (linter)",
			wantPath: "",
			wantOK:   false,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			path, ok := FindingPath(testCase.line)
			if ok != testCase.wantOK {
				t.Fatalf("FindingPath(%q) ok = %v, want %v", testCase.line, ok, testCase.wantOK)
			}
			if path != testCase.wantPath {
				t.Errorf("FindingPath(%q) path = %q, want %q", testCase.line, path, testCase.wantPath)
			}
		})
	}
}
