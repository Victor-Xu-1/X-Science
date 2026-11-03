package workspace

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"strings"
	"x-science/internal/literaturediscovery"
)

// Normalization and candidate identity are shared by Web and first-party tasks.
func normalizeLiteratureCandidateStage(input LiteratureCandidateStage) (LiteratureCandidateStage, error) {
	if input.Source != nil && (!validLiteratureID(input.Source.ItemID) || input.Source.MetadataRevision < 1 || (input.Source.Direction != "references" && input.Source.Direction != "citations")) {
		return LiteratureCandidateStage{}, ErrLiteratureInvalid
	}
	if !candidateString(input.Provider, 64) || !candidateString(input.ExternalID, 512) || strings.TrimSpace(input.Title) == "" || len(input.Title) > 4096 {
		return LiteratureCandidateStage{}, ErrLiteratureInvalid
	}
	raw, err := literatureMetadata(input.Metadata)
	if err != nil {
		return LiteratureCandidateStage{}, err
	}
	meta, err := decodeLiteratureMetadataObject(raw)
	if err != nil {
		return LiteratureCandidateStage{}, err
	}
	_, err = candidateIdentities(meta)
	if err != nil {
		return LiteratureCandidateStage{}, err
	}
	input.Metadata = meta
	return input, nil
}

func (s *Store) StageLiteratureCandidate(ctx context.Context, owner string, input LiteratureCandidateStage) (LiteratureCandidateReceipt, error) {
	normalized, err := normalizeLiteratureCandidateStage(input)
	if err != nil {
		return LiteratureCandidateReceipt{}, err
	}
	return s.candidateMutation(ctx, owner, input.OperationID, "stage", normalized, func(tx *sql.Tx) (LiteratureCandidate, error) {
		return s.stageLiteratureCandidateTx(ctx, tx, owner, normalized)
	})
}

func (s *Store) stageLiteratureCandidateTx(ctx context.Context, tx workspaceTransaction, owner string, input LiteratureCandidateStage) (LiteratureCandidate, error) {
	meta := input.Metadata
	ids, err := candidateIdentities(meta)
	if err != nil {
		return LiteratureCandidate{}, err
	}
	if input.Source != nil {
		var revision int64
		var deleted sql.NullString
		err := tx.QueryRowContext(ctx, "SELECT metadata_revision,deleted_at FROM literature_items WHERE id=? AND owner_user_id=?", input.Source.ItemID, owner).Scan(&revision, &deleted)
		if errors.Is(err, sql.ErrNoRows) || err == nil && deleted.Valid {
			return LiteratureCandidate{}, ErrLiteratureUnavailable
		}
		if err != nil {
			return LiteratureCandidate{}, err
		}
		if revision != input.Source.MetadataRevision {
			return LiteratureCandidate{}, ErrLiteratureConflict
		}
	}
	var alias sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT candidate_id FROM literature_candidate_sources WHERE owner_user_id=? AND provider=? AND external_id=?", owner, input.Provider, candidateSourceKey(input.Provider, input.ExternalID)).Scan(&alias); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return LiteratureCandidate{}, err
	}
	rows, e := tx.QueryContext(ctx, `SELECT id,provider,external_id,metadata_json FROM literature_candidates WHERE owner_user_id=?`, owner)
	if e != nil {
		return LiteratureCandidate{}, e
	}
	matching := alias.String
	for rows.Next() {
		var id, provider, external, raw string
		if e = rows.Scan(&id, &provider, &external, &raw); e != nil {
			break
		}
		meta, ee := decodeLiteratureMetadataObject(raw)
		if ee != nil {
			e = ee
			break
		}
		other, ee := candidateIdentities(meta)
		if ee != nil {
			e = ee
			break
		}
		same, ee := candidateIdentityMatch(ids, other)
		if ee != nil {
			e = ee
			break
		}
		if provider == input.Provider && (external == input.ExternalID || provider == "semantic-scholar" && strings.EqualFold(external, input.ExternalID)) {
			if candidateIdentityConflict(ids, other) {
				e = ErrLiteratureConflict
				break
			}
			same = true
		}
		if same {
			if matching != "" && matching != id {
				e = ErrLiteratureConflict
				break
			}
			matching = id
		}
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return LiteratureCandidate{}, e
	}
	if matching != "" {
		existing, err := readLiteratureCandidate(ctx, tx, owner, matching)
		if err != nil {
			return existing, err
		}
		if err = requireCandidateAcceptedItem(ctx, tx, owner, existing); err != nil {
			return existing, err
		}
		known, err := candidateIdentities(existing.Metadata)
		if err != nil {
			return existing, err
		}
		if candidateIdentityConflict(ids, known) {
			return existing, ErrLiteratureConflict
		}
		sources, err := readCandidateSources(ctx, tx, owner, existing)
		if err != nil {
			return existing, err
		}
		sources = append(sources, literatureCandidateSource{input.Provider, input.ExternalID})
		changed := false
		for scheme, value := range ids {
			if known[scheme] != "" {
				continue
			}
			if prior, present := existing.Metadata[scheme]; present && prior != "" {
				return existing, ErrLiteratureConflict
			}
			existing.Metadata[scheme] = value
			changed = true
		}
		if changed || existing.State == "pending" {
			merged, err := candidateIdentities(existing.Metadata)
			if err != nil {
				return existing, err
			}
			item, err := findMatchingLibraryItem(ctx, tx, owner, merged, sources...)
			if err != nil {
				return existing, err
			}
			if changed && item != "" && item != existing.ItemID {
				return existing, ErrLiteratureConflict
			}
			if !changed && existing.State == "pending" && item != "" {
				if _, err = tx.ExecContext(ctx, "UPDATE literature_candidates SET state='accepted',item_id=?,updated_at=? WHERE owner_user_id=? AND id=?", item, s.now().UTC(), owner, matching); err != nil {
					return existing, err
				}
				existing.State, existing.ItemID = "accepted", item
			}
		}
		if changed || existing.State != "accepted" {
			metadataMap, metadata, err := candidateLibraryMetadata(existing.Metadata, existing.Provider, existing.ExternalID)
			if err != nil {
				return existing, err
			}
			existing.Metadata = metadataMap
			var doi any
			if value, ok := existing.Metadata["doi"].(string); ok && value != "" {
				doi, _ = literaturediscovery.NormalizeDOI(value)
			}
			if doi == nil && known["doi"] != "" {
				doi = known["doi"]
			}
			if _, err = tx.ExecContext(ctx, "UPDATE literature_candidates SET metadata_json=?,doi_key=?,updated_at=? WHERE owner_user_id=? AND id=?", metadata, doi, s.now().UTC(), owner, matching); err != nil {
				return existing, err
			}
		}
		if err = recordCandidateSource(ctx, tx, owner, input.Provider, input.ExternalID, matching); err != nil {
			return existing, err
		}
		return existing, nil
	}
	_, raw, e := candidateLibraryMetadata(meta, input.Provider, input.ExternalID)
	if e != nil {
		return LiteratureCandidate{}, e
	}
	item, e := findMatchingLibraryItem(ctx, tx, owner, ids, literatureCandidateSource{input.Provider, input.ExternalID})
	if e != nil {
		return LiteratureCandidate{}, e
	}
	state := "pending"
	var itemValue any
	if item != "" {
		state = "accepted"
		itemValue = item
	}
	var doi any
	if ids["doi"] != "" {
		doi = ids["doi"]
	}
	id, now := uuid.NewString(), s.now().UTC()
	_, e = tx.ExecContext(ctx, `INSERT INTO literature_candidates(id,owner_user_id,provider,external_id,title,metadata_json,doi_key,state,item_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, owner, input.Provider, input.ExternalID, strings.TrimSpace(input.Title), raw, doi, state, itemValue, now, now)
	if e != nil {
		return LiteratureCandidate{}, e
	}
	if e = recordCandidateSource(ctx, tx, owner, input.Provider, input.ExternalID, id); e != nil {
		return LiteratureCandidate{}, e
	}
	return readLiteratureCandidate(ctx, tx, owner, id)
}
