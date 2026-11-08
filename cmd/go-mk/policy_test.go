package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const policyBaseline = ".golangci-lint-baseline.txt"

func lintPolicyConfiguration(t *testing.T, consumer string) {
	t.Helper()

	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(filepath.Join(repository, "golangci.yml"))
	if err != nil {
		t.Fatal(err)
	}

	writeConsumerFile(t, consumer, ".make/golangci.yml", string(contents))
}

func lintPolicyNotice(t *testing.T, identifier string) string {
	t.Helper()

	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(filepath.Join(repository, "notices.txt"))
	if err != nil {
		t.Fatal(err)
	}

	for line := range strings.SplitSeq(string(contents), "\n") {
		if strings.HasPrefix(line, identifier+"\t") {
			return line + "\n"
		}
	}

	t.Fatalf("notice %s does not exist", identifier)

	return ""
}

func lintPolicyMake(consumer, engine string, arguments ...string) (string, error) {
	const environmentCount = 2

	options := make([]string, 0, environmentCount+len(arguments))
	options = append(options,
		"GO_MK_BIN="+engine,
		"_GO_MK_NOTICES_FILE="+filepath.Join(consumer, "notices.txt"),
	)
	options = append(options, arguments...)

	return runConsumerMake(consumer, options...)
}
