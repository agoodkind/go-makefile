package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const acceptNewTarget = "staticcheck-extra-baseline-accept-new"

func TestBaselineTargetPrintsWhyTheGateIsClosed(t *testing.T) {
	consumerDir := newConsumerRepository(t, afterNoticeDate)
	baselinePath := filepath.Join(consumerDir, baselineFileName)

	output, err := runConsumerMake(consumerDir, acceptNewTarget)
	if err != nil {
		t.Fatalf("target failed with no confirm value: %v\n%s", err, output)
	}
	if !strings.Contains(output, "BASELINE_CONFIRM is not set to yes") {
		t.Fatalf("output lacks the confirm reason:\n%s", output)
	}

	output, err = runConsumerMake(
		consumerDir, acceptNewTarget,
		"BASELINE_CONFIRM=yes", "BASELINE_TOKEN=wrong", "BASELINE_TOKEN_CMD=echo right",
	)
	if err != nil {
		t.Fatalf("target failed with a wrong token: %v\n%s", err, output)
	}
	if !strings.Contains(output, "BASELINE_TOKEN does not match the token") {
		t.Fatalf("output lacks the token reason:\n%s", output)
	}
	if _, statErr := os.Stat(baselinePath); !os.IsNotExist(statErr) {
		t.Fatalf("baseline stat error = %v, want no baseline file while the gate is closed", statErr)
	}

	output, err = runConsumerMake(
		consumerDir, acceptNewTarget,
		"BASELINE_CONFIRM=yes", "BASELINE_TOKEN=right", "BASELINE_TOKEN_CMD=echo right",
	)
	if err != nil {
		t.Fatalf("target failed with a matching token: %v\n%s", err, output)
	}
	if strings.Contains(output, "no baseline was updated") {
		t.Fatalf("output reports a closed gate with a matching token:\n%s", output)
	}
	if baseline := readConsumerFile(t, consumerDir, baselineFileName); !strings.Contains(baseline, olderTestFinding) {
		t.Fatalf("baseline lacks the finding %s after an open gate:\n%s", olderTestFinding, baseline)
	}
}
