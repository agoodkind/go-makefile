package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCheckFailureReport(t *testing.T) {
	primaryScript := workflowStepScript(t, "Run primary checks")
	secondaryScript := workflowStepScript(t, "Run secondary checks")
	reportScript := workflowStepScript(t, "Report check failures")

	const primaryFailure = "lint-golangci\n\n  sample.go:7:2\n    exhauststruct: sample.Config is missing field Enabled\n"
	const secondaryFailure = "test\n\n  TestWidget: got false, want true\n"
	testCases := []struct {
		name             string
		primaryCommand   string
		secondaryCommand string
		primaryOutcome   string
		secondaryOutcome string
		wantOutput       string
	}{
		{
			name:             "reports primary failure output",
			primaryCommand:   shellOutputCommand(primaryFailure, 1),
			secondaryCommand: shellOutputCommand("secondary passed\n", 0),
			primaryOutcome:   "failure",
			secondaryOutcome: "success",
			wantOutput:       primaryFailure,
		},
		{
			name:             "reports secondary failure output",
			primaryCommand:   shellOutputCommand("primary passed\n", 0),
			secondaryCommand: shellOutputCommand(secondaryFailure, 1),
			primaryOutcome:   "success",
			secondaryOutcome: "failure",
			wantOutput:       secondaryFailure,
		},
		{
			name:             "reports both failure outputs in check order",
			primaryCommand:   shellOutputCommand(primaryFailure, 1),
			secondaryCommand: shellOutputCommand(secondaryFailure, 1),
			primaryOutcome:   "failure",
			secondaryOutcome: "failure",
			wantOutput:       primaryFailure + secondaryFailure,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			temporaryDirectory := t.TempDir()
			environment := map[string]string{
				"GO_MK_CI_PRIMARY_COMMAND":   testCase.primaryCommand,
				"GO_MK_CI_PRIMARY_OUTCOME":   testCase.primaryOutcome,
				"GO_MK_CI_PRIMARY_REPORT":    filepath.Join(temporaryDirectory, "primary.out"),
				"GO_MK_CI_SECONDARY_COMMAND": testCase.secondaryCommand,
				"GO_MK_CI_SECONDARY_OUTCOME": testCase.secondaryOutcome,
				"GO_MK_CI_SECONDARY_REPORT":  filepath.Join(temporaryDirectory, "secondary.out"),
			}

			runWorkflowScript(t, primaryScript, environment, testCase.primaryOutcome)
			runWorkflowScript(t, secondaryScript, environment, testCase.secondaryOutcome)
			output, err := workflowScriptOutput(reportScript, environment)
			if err == nil {
				t.Fatalf("report command succeeded, want nonzero\noutput:\n%s", output)
			}
			if got := string(output); got != testCase.wantOutput {
				t.Fatalf("report output = %q, want %q", got, testCase.wantOutput)
			}
		})
	}
}

func shellOutputCommand(output string, status int) string {
	return "printf '%s' " + shellQuote(output) + "; exit " + strconv.Itoa(status)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func runWorkflowScript(t *testing.T, script string, environment map[string]string, outcome string) {
	t.Helper()
	output, err := workflowScriptOutput(script, environment)
	if outcome == "failure" && err == nil {
		t.Fatalf("check command succeeded, want failure\noutput:\n%s", output)
	}
	if outcome == "success" && err != nil {
		t.Fatalf("check command failed: %v\noutput:\n%s", err, output)
	}
}

func workflowScriptOutput(script string, environment map[string]string) ([]byte, error) {
	command := exec.Command("bash", "-c", script)
	command.Env = testProcessEnvironment(environment)
	return command.CombinedOutput()
}

func workflowStepScript(t *testing.T, name string) string {
	t.Helper()
	workflowPath := filepath.Join(repoRootForTest(t), ".github", "workflows", "_ci.yml")
	workflowBytes, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read %s: %v", workflowPath, err)
	}
	step := workflowStep(t, string(workflowBytes), name)
	marker := "        run: |\n"
	start := strings.Index(step, marker)
	if start < 0 {
		t.Fatalf("%s has no inline shell block:\n%s", name, step)
	}
	lines := strings.Split(step[start+len(marker):], "\n")
	scriptLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if !strings.HasPrefix(line, "          ") {
			break
		}
		scriptLines = append(scriptLines, strings.TrimPrefix(line, "          "))
	}
	return strings.Join(scriptLines, "\n")
}

func workflowStep(t *testing.T, workflow string, name string) string {
	t.Helper()
	marker := "      - name: " + name + "\n"
	start := strings.Index(workflow, marker)
	if start < 0 {
		t.Fatalf("workflow missing step %q", name)
	}
	remainder := workflow[start+len(marker):]
	next := strings.Index(remainder, "\n      - ")
	if next < 0 {
		return workflow[start:]
	}
	return workflow[start : start+len(marker)+next]
}
