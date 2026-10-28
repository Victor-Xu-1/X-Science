package workspace

import (
	"context"
	"database/sql"
	"strings"
)

type literatureCandidateSource struct {
	Provider   string
	ExternalID string
}

// The canonical source is retained for candidates created before aliases were
// recorded. Every persisted alias is read inside the same owner-scoped write
// transaction used for the identity decision and its frozen mutation receipt.
func readCandidateSources(ctx context.Context, tx *sql.Tx, owner string, candidate LiteratureCandidate) ([]literatureCandidateSource, error) {
	sources := []literatureCandidateSource{{candidate.Provider, candidate.ExternalID}}
	rows, err := tx.QueryContext(ctx, `SELECT provider,external_id FROM literature_candidate_sources WHERE owner_user_id=? AND candidate_id=?`, owner, candidate.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var source literatureCandidateSource
		if err := rows.Scan(&source.Provider, &source.ExternalID); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

// Validate exactly the metadata that a future accept will persist. Clone the
// map so provider identity enrichment cannot mutate the original request used
// for the idempotency hash, nor silently replace an opaque metadata field.
func candidateLibraryMetadata(metadata map[string]any, provider, external string) (map[string]any, string, error) {
	final := make(map[string]any, len(metadata)+1)
	for key, value := range metadata {
		final[key] = value
	}
	if provider == "semantic-scholar" {
		value, present := final["semantic_scholar_id"]
		if present {
			text, valid := value.(string)
			if !valid || text != "" && !strings.EqualFold(text, external) {
				return nil, "", ErrLiteratureConflict
			}
			if text == "" {
				final["semantic_scholar_id"] = external
			}
		} else {
			final["semantic_scholar_id"] = external
		}
	}
	raw, err := literatureMetadata(final)
	return final, raw, err
}
