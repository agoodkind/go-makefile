// Package config reads the policy that go-mk embeds at build time.
package config

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	analyzerPrefix = " ("
	analyzerSuffix = ")"
)

//go:embed advisory.json
var advisoryJSON string

var (
	errPolicyObject = errors.New("advisory policy requires a JSON object")
	errCheckName    = errors.New("advisory check requires a name")

	advisory, advisoryError = decodeAdvisory(advisoryJSON)
)

// ValidateAdvisory reports errors in the embedded policy.
//
// Check the policy before executing a command.
//
//	err := ValidateAdvisory()
func ValidateAdvisory() error {
	return advisoryError
}

// AdvisoryCheck reports whether every finding from a check is advisory.
//
// Query this policy for tools that report findings without analyzer labels.
//
//	advisory := AdvisoryCheck("go-version")
func AdvisoryCheck(check string) bool {
	analyzers, ok := advisory[check]

	return ok && len(analyzers) == 0
}

// AdvisoryFinding reports whether a check allows a finding without failing.
//
// Golangci-lint adds the analyzer name in parentheses after its message.
//
//	advisory := AdvisoryFinding("golangci-lint", "func run is unused (unused)")
func AdvisoryFinding(check, finding string) bool {
	if AdvisoryCheck(check) {
		return true
	}

	for _, analyzer := range advisory[check] {
		if strings.HasSuffix(finding, analyzerPrefix+analyzer+analyzerSuffix) {
			return true
		}
	}

	return false
}

func decodeAdvisory(data string) (map[string][]string, error) {
	var policy map[string][]string
	if err := json.Unmarshal([]byte(data), &policy); err != nil {
		return nil, err
	}

	if policy == nil {
		return nil, errPolicyObject
	}

	for check, analyzers := range policy {
		if strings.TrimSpace(check) == "" {
			return nil, errCheckName
		}

		if analyzers == nil {
			return nil, fmt.Errorf("advisory check requires an analyzer array for %q", check)
		}

		for _, analyzer := range analyzers {
			if strings.TrimSpace(analyzer) == "" {
				return nil, fmt.Errorf("advisory analyzer requires a name for %q", check)
			}
		}
	}

	return policy, nil
}
