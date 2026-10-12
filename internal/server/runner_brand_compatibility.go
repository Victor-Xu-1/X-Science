package server

import "strings"

const (
	legacyDurableCorrectionContextMarker = "Synon durable correction state from a prior bounded execution unit"
	legacyNoProgressDetailMarker         = "\nSynon no-progress recovery state: "
)

// New writes use the current brand. Existing transcript/checkpoint bytes are
// immutable and retain their exact reader aliases under the same authority.
func hasSessionRunnerDurableCorrectionContext(content string) bool {
	return strings.Contains(content, sessionRunnerDurableCorrectionContextMarker) ||
		strings.Contains(content, legacyDurableCorrectionContextMarker)
}

func sessionRunnerNoProgressMarkerIndex(detail string, last bool) (int, int) {
	index, length := -1, 0
	for _, marker := range []string{sessionRunnerNoProgressDetailMarker, legacyNoProgressDetailMarker} {
		position := strings.Index(detail, marker)
		if last {
			position = strings.LastIndex(detail, marker)
		}
		if position >= 0 && (index < 0 || (last && position > index) || (!last && position < index)) {
			index, length = position, len(marker)
		}
	}
	return index, length
}
