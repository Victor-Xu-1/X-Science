package workspace

import "encoding/json"

const (
	literatureMetadataLegacyBytes   = 16 << 10
	literatureMetadataSnapshotBytes = 80 << 10
	literatureMetadataPatchBytes    = 96 << 10
	literaturePersonalNoteUnits     = 10000
)

// Freeze bounded request data before computing its existing immutable identity.
// New-write admission follows ledger lookup, so a compatible fallback can replay
// an original larger request without recreating or changing its outcome.
func normalizeLiteratureMetadata(value map[string]any) (string, error) {
	if value == nil {
		return "{}", nil
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > literatureMetadataSnapshotBytes {
		return "", ErrLiteratureInvalid
	}
	return string(raw), nil
}

// Larger catalog objects reserve a separate bounded string note budget. The
// remaining object still obeys the original complete metadata byte limit.
func validExpandedLiteratureMetadata(raw string) bool {
	if len(raw) > literatureMetadataSnapshotBytes {
		return false
	}
	metadata, err := decodeLiteratureMetadataObject(raw)
	if err != nil {
		return false
	}
	note, ok := metadata["personal_note"].(string)
	if !ok {
		return false
	}
	units := 0
	for _, char := range note {
		units++
		if char > 0xffff {
			units++
		}
		if units > literaturePersonalNoteUnits {
			return false
		}
	}
	delete(metadata, "personal_note")
	ordinary, err := json.Marshal(metadata)
	return err == nil && len(ordinary) <= literatureMetadataLegacyBytes
}

// This compatibility writer does not introduce the first large catalog row.
// Discovery candidate staging keeps its separate historical metadata admission.
func admitNewLiteratureCatalogMetadata(raw string) error {
	if len(raw) > literatureMetadataLegacyBytes {
		return ErrLiteratureInvalid
	}
	return nil
}

// Only the canonical prior row, read in the existing CAS transaction, can
// authorize continued large writes after rollback from a capable new writer.
func admitLiteratureCatalogMetadataUpdate(prior, next string) error {
	if err := admitNewLiteratureCatalogMetadata(next); err == nil {
		return nil
	}
	if len(prior) <= literatureMetadataLegacyBytes || !validExpandedLiteratureMetadata(prior) || !validExpandedLiteratureMetadata(next) {
		return ErrLiteratureInvalid
	}
	return nil
}
