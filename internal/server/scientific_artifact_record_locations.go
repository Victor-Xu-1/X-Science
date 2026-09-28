package server

const maxScientificArtifactRecordLocations = 32

// Record is the one-based parser record ordinal. Line, when present, counts
// physical text lines including headers, comments and blank lines.
type scientificArtifactRecordLocation struct {
	Record int `json:"record"`
	Line   int `json:"line,omitempty"`
}

func scientificArtifactRecordLocationsValid(result scientificArtifactValidation) bool {
	locations := result.InvalidRecordLocations
	if len(locations) > maxScientificArtifactRecordLocations || len(locations) > result.InvalidRecordCount {
		return false
	}
	if len(locations) > 0 && (result.OK || result.RDKitVersion == "") {
		return false
	}
	previous := scientificArtifactRecordLocation{}
	for _, location := range locations {
		if location.Record <= previous.Record || location.Record > result.SupplierRecordCount {
			return false
		}
		if result.Format == "smi" {
			if location.Line <= previous.Line || location.Line < location.Record {
				return false
			}
		} else if location.Line != 0 {
			return false
		}
		previous = location
	}
	return true
}
