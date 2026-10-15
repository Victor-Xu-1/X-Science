package server

import (
	"strconv"
	"strings"
)

// Only the exact host-generated aggregate observation can release an old typed
// correction. Per-file record_count_mismatch and unknown diagnostics are not
// evidence of this heuristic, even when their presentation mentions counts.
func scientificArtifactAggregateCountFindingIsAdvisory(finding string) bool {
	body, found := strings.CutPrefix(finding, "molecule_set_record_count_mismatch:")
	if !found {
		return false
	}
	separator := strings.LastIndex(body, " sdf=")
	if separator <= 0 || strings.TrimSpace(body[:separator]) == "" {
		return false
	}
	sdfText, smilesText, found := strings.Cut(body[separator+len(" sdf="):], " smiles=")
	if !found {
		return false
	}
	sdf, sdfErr := strconv.ParseInt(sdfText, 10, strconv.IntSize)
	smiles, smilesErr := strconv.ParseInt(smilesText, 10, strconv.IntSize)
	return sdfErr == nil && smilesErr == nil && sdf >= 0 && smiles >= 0 && sdf != smiles &&
		strconv.FormatInt(sdf, 10) == sdfText && strconv.FormatInt(smiles, 10) == smilesText
}
