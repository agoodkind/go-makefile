package main_test

import (
	"os"
	"strings"
	"testing"
)

const initializationTypes = `package model

import "net/http"

type Record struct {
	Name string
	Ready bool
}

type Envelope struct { Record }

type Server = http.Server
`

// TestExhaustructV5Selection checks internal, aliased, empty, and
// promoted literals.
func TestExhaustructV5Selection(test *testing.T) {
	engine := noticeGitBuildEngine(test, os.Environ())

	for _, scenario := range []struct {
		name        string
		declaration string
		fails       bool
	}{
		{"complete internal", `func Value() Record { return Record{Name: "ready", Ready: true} }`, false},
		{"partial internal", `func Value() Record { return Record{Name: "partial"} }`, true},
		{"empty internal", `func Value() Record { return Record{} }`, true},
		{"empty error return", `func Value(err error) (Record, error) { return Record{}, err }`, false},
		{"empty nil return", `func Value() (Record, error) { return Record{}, nil }`, true},
		{"external type", `func Value() http.Server { return http.Server{Addr: ":0"} }`, false},
		{"internal alias", `func Value() Server { return Server{Addr: ":0"} }`, true},
		{"complete promoted", `func Value() Envelope { return Envelope{Name: "ready", Ready: true} }`, false},
		{"partial promoted", `func Value() Envelope { return Envelope{Name: "partial"} }`, true},
	} {
		test.Run(scenario.name, func(child *testing.T) {
			consumer := initializationConsumer(child)
			writeConsumerFile(child, consumer, "internal/model/model.go", initializationTypes+scenario.declaration+"\n")
			output, err := lintPolicyMake(consumer, engine,
				"lint-golangci", "GOLANGCI_LINT_RUN_FLAGS=-c .make/golangci.yml --enable-only=exhaustruct_v5")

			if scenario.fails {
				if err == nil || !strings.Contains(output, "(exhaustruct_v5)") {
					child.Fatalf("incomplete selected literal did not fail: %v\n%s", err, output)
				}

				return
			}

			if err != nil {
				child.Fatalf("allowed literal failed: %v\n%s", err, output)
			}
		})
	}
}

// TestExhaustructV5UsesAppliedNotice checks historical adoption and later
// literal rejection.
func TestExhaustructV5UsesAppliedNotice(test *testing.T) {
	engine := noticeGitBuildEngine(test, os.Environ())
	consumer := initializationConsumer(test)
	writeConsumerFile(test, consumer, "notices.txt", lintPolicyNotice(test, "11"))
	writeConsumerFile(test, consumer, "internal/model/model.go", initializationTypes+`
func Historical() Record { return Record{Name: "historical"} }
`)
	runConsumerGit(test, consumer, beforeNoticeDate, "add", ".")
	runConsumerGit(test, consumer, beforeNoticeDate, "commit", "--quiet", "--message", "Add historical internal literal")

	output, err := lintPolicyMake(consumer, engine, "go-mk-notice")
	if err != nil {
		test.Fatalf("apply v5 notice: %v\n%s", err, output)
	}

	baseline := readConsumerFile(test, consumer, policyBaseline)
	if !strings.Contains(baseline, "internal/model/model.go:") || !strings.Contains(baseline, "(exhaustruct_v5)") {
		test.Fatalf("notice did not record the historical literal:\n%s\n%s", baseline, output)
	}

	writeConsumerFile(test, consumer, "internal/model/later.go", `package model

func Later() Record { return Record{Name: "later"} }
`)
	output, err = lintPolicyMake(consumer, engine,
		"lint-golangci", "GOLANGCI_LINT_RUN_FLAGS=-c .make/golangci.yml --enable-only=exhaustruct_v5")

	if err == nil || !strings.Contains(output, "internal/model/later.go:") {
		test.Fatalf("a later incomplete literal did not fail: %v\n%s", err, output)
	}
}

func initializationConsumer(t *testing.T) string {
	t.Helper()

	consumer := newConsumerRepository(t, beforeNoticeDate)
	lintPolicyConfiguration(t, consumer)
	writeConsumerFile(t, consumer, "go.mod", "module goodkind.io/lint-policy-fixture\n\ngo 1.27.3\n")

	return consumer
}
