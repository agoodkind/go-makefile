package report

import (
	"encoding/json"
	"strings"
)

// MarkerPrefix begins the one machine-readable line a gate child emits so the
// parent can collect its result without parsing human text. The leading unit
// separator (0x1f) cannot appear in lint output, so the parent recognizes the
// line unambiguously and strips it before printing anything.
const MarkerPrefix = "\x1fGOMK-RESULT\x1f"

// GateMarker is the wire form of one gate child's outcome: the gate's own
// verdict and findings, computed by lintgate and unchanged. The parent decodes
// it into a StepResult. Findings holds the raw finding strings; the parent
// formats them for display so the gate's detection stays untouched.
type GateMarker struct {
	Name        string   `json:"name"`
	Passed      bool     `json:"passed"`
	Findings    []string `json:"findings"`
	Remediation string   `json:"remediation"`
}

// EncodeMarker renders a marker as one prefixed line with no embedded newlines,
// suitable for writing to the child's stderr.
func EncodeMarker(marker GateMarker) (string, error) {
	payload, err := json.Marshal(marker)
	if err != nil {
		return "", err
	}
	return MarkerPrefix + string(payload), nil
}

// ParseMarker decodes a marker line. The second result is false when the line is
// not a marker, so the caller can scan captured output and act only on hits.
func ParseMarker(line string) (GateMarker, bool) {
	rest, ok := strings.CutPrefix(line, MarkerPrefix)
	if !ok {
		return GateMarker{}, false
	}
	var marker GateMarker
	if err := json.Unmarshal([]byte(rest), &marker); err != nil {
		return GateMarker{}, false
	}
	return marker, true
}
