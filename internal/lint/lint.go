// Package lint ports the pure helpers from scripts/go-mk-lint.sh and the lint
// portions of scripts/go-mk-common.sh into Go. It holds the string-shaping
// logic the lint gates need: the exclude and scope regex builders, the
// golangci and staticcheck scope-pattern resolvers, the gocyclo awk transform,
// the scoped-package and scoped-finding filters, and the make-error-line
// filter the chain runner applies to aggregated output. The package never
// touches the filesystem, the clock, or the process streams: the command layer
// in cmd/go-mk owns running tools, reading and writing files, and printing,
// mirroring the internal/findings, internal/capture, and internal/lintgate
// split. Output is shaped to match the shell byte-for-byte.
//
// =============================================================================
// lint
// =============================================================================
package lint

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ExcludePattern joins the default and extra exclude path patterns into a
// single alternation, mirroring go_mk_exclude_pattern: the two comma-separated
// lists are concatenated with a comma, split on commas, the non-empty fields
// are kept, and the result is joined with "|". An all-empty input yields the
// empty string, which the caller treats as the no-filter pass-through.
//
// ---- ExcludePattern ----
func ExcludePattern(defaultPatterns, extraPatterns string) string {
	combined := defaultPatterns + "," + extraPatterns
	fields := make([]string, 0)
	for _, field := range strings.Split(combined, ",") {
		if field != "" {
			fields = append(fields, field)
		}
	}
	return strings.Join(fields, "|")
}

// GolangciScopePattern resolves the grep -E scope regex for a scoped
// golangci-lint baseline or run, mirroring go_mk_golangci_baseline_scope_pattern.
// An explicit pattern wins; otherwise a RULE name is the narrowest scope and
// matches a meta-linter sub-rule via its "name:" message prefix, bound to the
// linter tag when LINTER is also set; otherwise a LINTER name matches the whole
// linter via its trailing "(name)" tag. RULE wins over LINTER. The regex is
// built from the supplied names, so this never needs the set of known linters.
//
// ---- GolangciScopePattern ----
func GolangciScopePattern(explicit, rule, linter string) string {
	if explicit != "" {
		return explicit
	}
	if rule != "" {
		if linter != "" {
			return rule + `:.*\(` + linter + `\)$`
		}
		return rule + ":"
	}
	if linter != "" {
		return `\(` + linter + `\)$`
	}
	return ""
}

// GocycloTransform reproduces the gocyclo awk transform: each raw gocyclo line
// with at least four fields becomes "<location>: gocyclo: complexity <c> over
// <threshold> in <symbol>", where the complexity is the first field, the
// location is the last field, and the symbol is the fields in between joined by
// single spaces. Lines with fewer than four fields are dropped. The returned
// slice preserves input order and is never nil.
//
// ---- GocycloTransform ----
func GocycloTransform(lines []string, threshold int) []string {
	out := make([]string, 0, len(lines))
	thresholdText := strconv.Itoa(threshold)
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		complexity := fields[0]
		location := fields[len(fields)-1]
		symbol := strings.Join(fields[1:len(fields)-1], " ")
		out = append(out, location+": gocyclo: complexity "+complexity+" over "+thresholdText+" in "+symbol)
	}
	return out
}

// ScopedPackagesFromFiles maps a list of file paths to the sorted, unique set
// of "./<dir>" package directories, mirroring scoped_packages_from_files: each
// file's directory is taken, deduplicated, sorted, and prefixed with "./". The
// returned slice is never nil.
//
// ---- ScopedPackagesFromFiles ----
func ScopedPackagesFromFiles(files []string) []string {
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if file == "" {
			continue
		}
		directory := path.Dir(file)
		seen["./"+directory] = struct{}{}
	}
	packages := make([]string, 0, len(seen))
	for pkg := range seen {
		packages = append(packages, pkg)
	}
	sort.Strings(packages)
	return packages
}

// FilterScopedFindings keeps the finding lines whose path begins with one of
// the listed files followed by a colon, mirroring filter_scoped_findings: the
// awk keeps a line when index($0, file ":") == 1, i.e. the line starts with the
// file path and a colon. The returned slice preserves input order and is never
// nil.
//
// ---- FilterScopedFindings ----
func FilterScopedFindings(lines, files []string) []string {
	keep := make([]string, 0)
	for _, file := range files {
		if file != "" {
			keep = append(keep, file+":")
		}
	}
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		for _, prefix := range keep {
			if strings.HasPrefix(line, prefix) {
				kept = append(kept, line)
				break
			}
		}
	}
	return kept
}

// makeErrorLine matches the recursive-make error summary lines the chain runner
// strips from aggregated gate output, mirroring the awk negation
// !/^make(\[[0-9]+\])?: \*\*\* \[[^]]+\] Error [0-9]+$/.
var makeErrorLine = regexp.MustCompile(`^make(\[[0-9]+\])?: \*\*\* \[[^]]+\] Error [0-9]+$`)

// FilterMakeErrorLines drops the recursive-make "*** [target] Error N" summary
// lines from the aggregated lint output, mirroring the awk filter in
// run_lint_chain. The returned slice preserves input order and is never nil.
//
// ---- FilterMakeErrorLines ----
func FilterMakeErrorLines(lines []string) []string {
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if makeErrorLine.MatchString(line) {
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

// DedupeFailedGates returns the failed gate names in first-seen order with
// duplicates removed, mirroring the awk that builds the "Failed gates:" list
// from .make/lint.failed. The returned slice is never nil.
//
// ---- DedupeFailedGates ----
func DedupeFailedGates(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// Slugify reproduces go_mk_slugify for the bypass-token comparison: it keeps
// only ASCII letters, digits, underscore, and hyphen, and lowercases letters.
// The shell first transliterates UTF-8 to ASCII via iconv, which this
// approximates by dropping any byte outside the kept set; for the ASCII tokens
// the gate compares this matches the shell exactly.
//
// ---- Slugify ----
func Slugify(text string) string {
	var builder strings.Builder
	for _, char := range text {
		switch {
		case char >= 'A' && char <= 'Z':
			builder.WriteRune(char + ('a' - 'A'))
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '_' || char == '-':
			builder.WriteRune(char)
		}
	}
	return builder.String()
}
