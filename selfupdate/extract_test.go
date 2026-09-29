package selfupdate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestValidateCandidateChecksDarwinSignatureBeforeExecution(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin-only candidate validation order test")
	}
	tempDir := t.TempDir()
	markerPath := filepath.Join(tempDir, "executed")
	candidatePath := filepath.Join(tempDir, "agent-gate")
	content := "#!/usr/bin/env bash\n" +
		"printf 'ran' > " + strconv.Quote(markerPath) + "\n" +
		"printf 'version: unsigned\\n'\n"
	if err := os.WriteFile(candidatePath, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	err := validateCandidate(context.Background(), testConfig(), candidatePath)
	if err == nil {
		t.Fatal("validateCandidate() error = nil, want unsigned candidate failure")
	}
	if !strings.Contains(err.Error(), "codesign verify failed") {
		t.Fatalf("validateCandidate() error = %v", err)
	}
	if _, statErr := os.Stat(markerPath); !os.IsNotExist(statErr) {
		t.Fatalf("candidate executed before signature verification; stat marker = %v", statErr)
	}
}
