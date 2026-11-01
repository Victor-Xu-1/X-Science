package server

import "errors"

const annotationNoteMaxUTF16Units = 4000

// Web note editors count JavaScript string.length. Astral characters therefore
// consume two UTF16 units. Preserve the exact content: callers retain their own
// trimming/blank-note behavior; this boundary never trims or truncates a note.
func annotationNoteText(value any) (string, error) {
	text, ok := value.(string)
	if !ok || text == "" {
		return "", errors.New("text is required (1-4000 UTF16 code units)")
	}
	units := 0
	for _, character := range text {
		units++
		if character > 0xffff {
			units++
		}
		if units > annotationNoteMaxUTF16Units {
			return "", errors.New("text is required (1-4000 UTF16 code units)")
		}
	}
	return text, nil
}
