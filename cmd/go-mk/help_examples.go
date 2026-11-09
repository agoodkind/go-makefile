package main

import (
	"cmp"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
)

//go:embed help_examples/*.json
var helpExampleFiles embed.FS

type ruleExample struct {
	Tool    string `json:"tool"`
	ID      string `json:"id"`
	Summary string `json:"summary"`
	Bad     string `json:"bad"`
	Good    string `json:"good"`
}

func runHelpExamples() int {
	linter := strings.TrimSpace(os.Getenv("LINTER"))
	rule := strings.TrimSpace(os.Getenv("RULE"))
	examples, err := loadRuleExamples()
	if err == nil {
		examples, err = selectRuleExamples(examples, linter, rule)
	}

	if err != nil {
		writeStderr("help-examples: " + err.Error() + "\n")
		return 1
	}

	if len(examples) == 1 && (linter != "" || rule != "") {
		writeStdout(renderRuleExamples(examples))
	} else {
		writeStdout(renderRuleExampleChoices(examples))
	}

	return 0
}

func loadRuleExamples() (examples []ruleExample, err error) {
	defer func() {
		if err != nil {
			slog.Error("load built-in rule examples failed", slog.Any("err", err))
		}
	}()
	files, err := helpExampleFiles.ReadDir("help_examples")
	if err != nil {
		return nil, fmt.Errorf("cannot read the built-in rule catalog: %w", err)
	}

	identifiers := make(map[string]bool)

	for _, file := range files {
		data, readErr := helpExampleFiles.ReadFile("help_examples/" + file.Name())
		if readErr != nil {
			return nil, fmt.Errorf("cannot read rule examples from %s: %w", file.Name(), readErr)
		}

		var entries []ruleExample
		if decodeErr := json.Unmarshal(data, &entries); decodeErr != nil {
			return nil, fmt.Errorf(
				"cannot decode rule examples from %s: %w",
				file.Name(),
				decodeErr,
			)
		}

		for _, entry := range entries {
			if validateErr := validateRuleExample(entry, identifiers); validateErr != nil {
				return nil, fmt.Errorf("invalid rule catalog %s: %w", file.Name(), validateErr)
			}

			examples = append(examples, entry)
		}
	}

	if len(examples) == 0 {
		return nil, fmt.Errorf("the built-in rule catalog is empty")
	}

	slices.SortFunc(
		examples,
		func(left, right ruleExample) int {
			if left.Tool != right.Tool {
				return cmp.Compare(left.Tool, right.Tool)
			}

			return cmp.Compare(left.ID, right.ID)
		},
	)

	return examples, nil
}

func validateRuleExample(entry ruleExample, identifiers map[string]bool) error {
	identifier := entry.Tool + "/" + entry.ID
	if strings.TrimSpace(entry.Tool) == "" || strings.TrimSpace(entry.ID) == "" ||
		strings.TrimSpace(
			entry.Summary,
		) == "" || strings.TrimSpace(entry.Bad) == "" || strings.TrimSpace(entry.Good) == "" {
		return fmt.Errorf(
			"rule %q requires a tool, identifier, explanation, bad example, and good example",
			identifier,
		)
	}

	if identifiers[identifier] {
		return fmt.Errorf("rule %q appears more than once", identifier)
	}

	identifiers[identifier] = true

	return nil
}

func selectRuleExamples(examples []ruleExample, linter, rule string) ([]ruleExample, error) {
	var exact, partial []ruleExample

	for _, entry := range examples {
		if linter != "" && entry.Tool != linter &&
			strings.TrimPrefix(entry.Tool, "golangci-lint/") != linter {
			continue
		}

		if rule == entry.Tool+"/"+entry.ID || rule == entry.ID {
			exact = append(exact, entry)
		} else if strings.Contains(strings.ToLower(entry.ID), strings.ToLower(rule)) {
			partial = append(partial, entry)
		}
	}

	if len(exact) > 0 {
		return exact, nil
	}

	if len(partial) > 0 {
		return partial, nil
	}

	return nil, fmt.Errorf(
		"no examples match LINTER=%q RULE=%q; run make help-examples without filters to list available rules",
		linter,
		rule,
	)
}

func renderRuleExampleChoices(examples []ruleExample) string {
	var output strings.Builder
	output.WriteString("Available rule examples:\n\n")

	for _, entry := range examples {
		output.WriteString("  " + entry.Tool + "/" + entry.ID + "  " + entry.Summary + "\n")
	}

	output.WriteString("\nSet LINTER or RULE to narrow the list.\n")
	output.WriteString(
		"Print one example with make help-examples RULE=" + examples[0].Tool + "/" + examples[0].ID + "\n",
	)

	return output.String()
}

func renderRuleExamples(examples []ruleExample) string {
	var output strings.Builder

	for index, entry := range examples {
		if index > 0 {
			output.WriteByte('\n')
		}

		output.WriteString(entry.Tool + "/" + entry.ID + "\n")
		output.WriteString(entry.Summary + "\n\n")
		output.WriteString("Bad:\n" + indentRuleExample(entry.Bad))
		output.WriteString("\nGood:\n" + indentRuleExample(entry.Good))
	}

	return output.String()
}

func indentRuleExample(code string) string {
	return "    " + strings.ReplaceAll(strings.TrimRight(code, "\n"), "\n", "\n    ") + "\n"
}
