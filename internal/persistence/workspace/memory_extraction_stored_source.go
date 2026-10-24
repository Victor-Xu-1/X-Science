package workspace

import "encoding/json"

type extractionStoredSource struct{ key, raw, digest string }

// Validate the row selected by SQL before using the original strict reader.
// In particular, a corrupt X row must not redirect that reader to a valid Y.
// Re-encoding also rejects duplicate keys/noncanonical JSON before Go and
// SQLite can disagree on the selected authority or its ordering fields.
func identifyStoredExtractionSource(current extractionSourceIdentity, stored extractionStoredSource) (extractionSourceIdentity, error) {
	var candidate CompletedExtractionSource
	if json.Unmarshal([]byte(stored.raw), &candidate) != nil {
		return extractionSourceIdentity{}, ErrExtractionPlanConflict
	}
	prior, err := identifyExtractionSource(candidate)
	if err != nil || prior.key != stored.key || prior.encoded != stored.raw || prior.digest != stored.digest ||
		prior.source.OwnerUserID != current.source.OwnerUserID || prior.source.ProjectID != current.source.ProjectID ||
		prior.source.FrameID != current.source.FrameID || prior.incarnation != current.incarnation {
		return extractionSourceIdentity{}, ErrExtractionPlanConflict
	}
	if proof := current.source.Transcript; proof != nil {
		if prior.source.Transcript == nil || prior.source.Legacy != nil ||
			prior.source.Transcript.StreamUID != proof.StreamUID || prior.source.Transcript.StreamEpoch != proof.StreamEpoch {
			return extractionSourceIdentity{}, ErrExtractionPlanConflict
		}
	} else if prior.source.Legacy == nil || prior.source.Transcript != nil ||
		prior.source.Legacy.SessionID != current.source.Legacy.SessionID {
		return extractionSourceIdentity{}, ErrExtractionPlanConflict
	}
	return prior, nil
}
