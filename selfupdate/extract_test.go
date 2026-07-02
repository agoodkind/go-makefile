package selfupdate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestReplaceBinaryDoesNotUseFixedTempPath(t *testing.T) {
	originalTimeNow := timeNow
	t.Cleanup(func() {
		timeNow = originalTimeNow
	})
	fixedTime := time.Unix(0, 123456789)
	timeNow = func() time.Time {
		return fixedTime
	}

	tempDir := t.TempDir()
	candidatePath := filepath.Join(tempDir, "candidate")
	if err := os.WriteFile(candidatePath, []byte("new binary"), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	installPath := filepath.Join(tempDir, "agent-gate")
	predictableTempPath := filepath.Join(
		tempDir,
		".agent-gate-update-"+strconv.FormatInt(fixedTime.UnixNano(), 10),
	)
	if err := os.WriteFile(predictableTempPath, []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	err := replaceBinary(candidatePath, installPath)
	if err != nil {
		t.Fatalf("replaceBinary() error: %v", err)
	}
	assertFileBytes(t, installPath, []byte("new binary"))
	assertFileBytes(t, predictableTempPath, []byte("sentinel"))
	info, err := os.Stat(installPath)
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("installed mode = %o, want 755", info.Mode().Perm())
	}
}
