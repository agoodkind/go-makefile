package notice

import (
	"net/url"
	"regexp"
	"strings"
)

var legacyAssignment = regexp.MustCompile(`^(GO_MK_URL|GO_MK)\s*(?::=|\?=|=)\s*(\S+)\s*$`)

var legacyConditional = regexp.MustCompile(`^(?:ifeq|ifneq|ifdef|ifndef)(?:[ \t]|$)`)

func legacyMakefile(contents string) bool {
	contents = strings.ReplaceAll(contents, "\r\n", "\n")
	contents = strings.ReplaceAll(contents, "\\\n", " ")
	values := make(map[string]string)
	included := false
	fetched := false
	definitionDepth := 0
	conditionalDepth := 0
	sharedTarget := false

	for raw := range strings.SplitSeq(contents, "\n") {
		line, _, _ := strings.Cut(raw, "#")
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "define ") {
			definitionDepth++

			continue
		}

		if line == "endef" && definitionDepth > 0 {
			definitionDepth--

			continue
		}

		if definitionDepth != 0 || line == "" {
			continue
		}

		if legacyConditional.MatchString(line) {
			conditionalDepth++

			continue
		}

		if line == "endif" && conditionalDepth > 0 {
			conditionalDepth--

			continue
		}

		if conditionalDepth != 0 {
			continue
		}

		if strings.HasPrefix(raw, "\t") {
			fetched = fetched || (sharedTarget && legacyFetch(line))

			continue
		}

		sharedTarget = line == "$(GO_MK):"

		if assignment := legacyAssignment.FindStringSubmatch(line); assignment != nil {
			values[assignment[1]] = assignment[2]
		}

		included = included || line == "include $(GO_MK)" || line == "-include $(GO_MK)" ||
			line == "sinclude $(GO_MK)"
	}

	return included && fetched && values["GO_MK"] == ".make/go.mk" && legacyURL(values["GO_MK_URL"])
}

func legacyFetch(line string) bool {
	line = strings.TrimPrefix(line, "@")
	if !strings.HasPrefix(line, "if curl ") && !strings.HasPrefix(line, "curl ") {
		return false
	}

	return strings.Contains(line, "$(GO_MK_URL)") && strings.Contains(line, `-o "$@"`)
}

func legacyURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}

	if parsed.Scheme != "https" || parsed.Host != "raw.githubusercontent.com" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}

	ref, prefix := strings.CutPrefix(parsed.Path, "/agoodkind/go-makefile/")
	ref, suffix := strings.CutSuffix(ref, "/go.mk")
	if !prefix || !suffix || ref == "" {
		return false
	}

	for segment := range strings.SplitSeq(ref, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}

	return true
}
