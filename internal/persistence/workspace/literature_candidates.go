package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"strings"
	"x-science/internal/literaturediscovery"
)

type LiteratureCandidate struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	Metadata   map[string]any `json:"metadata"`
	Provider   string         `json:"provider"`
	ExternalID string         `json:"external_id"`
	State      string         `json:"state"`
	ItemID     string         `json:"item_id,omitempty"`
}

// Preserve opaque metadata numbers in frozen mutation-ledger replays.
func (candidate *LiteratureCandidate) UnmarshalJSON(raw []byte) error {
	type wireCandidate LiteratureCandidate
	var decoded struct {
		*wireCandidate
		Metadata json.RawMessage `json:"metadata"`
	}
	var result wireCandidate
	decoded.wireCandidate = &result
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	metadata, err := decodeLiteratureMetadataObject(string(decoded.Metadata))
	if err != nil {
		return err
	}
	result.Metadata = metadata
	*candidate = LiteratureCandidate(result)
	return nil
}

type LiteratureCandidateDiscoverySource struct {
	ItemID           string `json:"item_id"`
	MetadataRevision int64  `json:"metadata_revision"`
	Direction        string `json:"direction"`
}

type LiteratureCandidateStage struct {
	OperationID string                              `json:"operation_id"`
	Provider    string                              `json:"provider"`
	ExternalID  string                              `json:"external_id"`
	Title       string                              `json:"title"`
	Metadata    map[string]any                      `json:"metadata"`
	Source      *LiteratureCandidateDiscoverySource `json:"source,omitempty"`
}
type LiteratureCandidateAction struct {
	OperationID string `json:"operation_id"`
}
type LiteratureCandidateReceipt struct {
	Schema      string              `json:"schema"`
	OperationID string              `json:"operation_id"`
	Action      string              `json:"action"`
	Candidate   LiteratureCandidate `json:"candidate"`
}

func readLiteratureCandidate(ctx context.Context, q literatureItemQuery, owner, id string) (LiteratureCandidate, error) {
	var c LiteratureCandidate
	var raw string
	var item sql.NullString
	err := q.QueryRowContext(ctx, `SELECT id,title,metadata_json,provider,external_id,state,item_id FROM literature_candidates WHERE owner_user_id=? AND id=?`, owner, id).Scan(&c.ID, &c.Title, &raw, &c.Provider, &c.ExternalID, &c.State, &item)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrLiteratureUnavailable
	}
	if err != nil {
		return c, err
	}
	c.ItemID = item.String
	c.Metadata, err = decodeLiteratureMetadataObject(raw)
	return c, err
}

func requireCandidateAcceptedItem(ctx context.Context, tx *sql.Tx, owner string, c LiteratureCandidate) error {
	if c.State != "accepted" {
		return nil
	}
	var deleted sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT deleted_at FROM literature_items WHERE id=? AND owner_user_id=?`, c.ItemID, owner).Scan(&deleted)
	if errors.Is(err, sql.ErrNoRows) || err == nil && deleted.Valid {
		return ErrLiteratureConflict
	}
	return err
}

type LiteratureCandidatePage struct {
	Schema     string                `json:"schema"`
	Candidates []LiteratureCandidate `json:"candidates"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

func (s *Store) ListLiteratureCandidates(ctx context.Context, owner, state, cursor string) (LiteratureCandidatePage, error) {
	page := LiteratureCandidatePage{Schema: "x-science.literature-candidates.v1", Candidates: []LiteratureCandidate{}}
	if !validLiteratureID(owner) || (state != "pending" && state != "dismissed") || (cursor != "" && !validLiteratureID(cursor)) {
		return page, ErrLiteratureInvalid
	}
	rows, err := s.readDatabase().QueryContext(ctx, `SELECT id,title,metadata_json,provider,external_id,state,item_id FROM literature_candidates WHERE owner_user_id=? AND state=? AND id>? ORDER BY id LIMIT 101`, owner, state, cursor)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var c LiteratureCandidate
		var raw string
		var item sql.NullString
		if err = rows.Scan(&c.ID, &c.Title, &raw, &c.Provider, &c.ExternalID, &c.State, &item); err != nil {
			return page, err
		}
		if len(page.Candidates) == 100 {
			page.NextCursor = page.Candidates[99].ID
			break
		}
		c.ItemID = item.String
		c.Metadata, err = decodeLiteratureMetadataObject(raw)
		if err != nil {
			return page, err
		}
		page.Candidates = append(page.Candidates, c)
	}
	return page, rows.Err()
}

func (s *Store) candidateMutation(ctx context.Context, owner, operation, action string, request any, apply func(*sql.Tx) (LiteratureCandidate, error)) (LiteratureCandidateReceipt, error) {
	var receipt LiteratureCandidateReceipt
	if !validLiteratureID(owner) || !validLiteratureID(operation) {
		return receipt, ErrLiteratureInvalid
	}
	hash, err := mutationRequestHash(request)
	if err != nil {
		return receipt, err
	}
	kind := "literature.candidate." + action
	if found, e := s.lookupMutationResult(ctx, owner, operation, kind, hash, &receipt); e != nil || found {
		return receipt, e
	}
	err = s.WithTransaction(ctx, func(tx *sql.Tx) error {
		found, e := lookupMutationResultTx(ctx, tx, owner, operation, kind, hash, &receipt)
		if e != nil || found {
			return e
		}
		candidate, e := apply(tx)
		if e != nil {
			return e
		}
		receipt = LiteratureCandidateReceipt{Schema: "x-science.literature-candidate-receipt.v1", OperationID: operation, Action: action, Candidate: candidate}
		return insertMutationResultTx(ctx, tx, owner, operation, kind, hash, receipt, s.now().UTC())
	})
	if err != nil {
		return LiteratureCandidateReceipt{}, err
	}
	return receipt, nil
}

func candidateSourceKey(provider, external string) string {
	if provider == "semantic-scholar" {
		return strings.ToLower(external)
	}
	return external
}
func recordCandidateSource(ctx context.Context, tx *sql.Tx, owner, provider, external, candidateID string) error {
	var valid bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM literature_candidates WHERE owner_user_id=? AND id=?)", owner, candidateID).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrLiteratureUnavailable
	}
	key := candidateSourceKey(provider, external)
	if _, err := tx.ExecContext(ctx, "INSERT INTO literature_candidate_sources(owner_user_id,provider,external_id,candidate_id) VALUES(?,?,?,?) ON CONFLICT(owner_user_id,provider,external_id) DO NOTHING", owner, provider, key, candidateID); err != nil {
		return err
	}
	var existing string
	if err := tx.QueryRowContext(ctx, "SELECT candidate_id FROM literature_candidate_sources WHERE owner_user_id=? AND provider=? AND external_id=?", owner, provider, key).Scan(&existing); err != nil {
		return err
	}
	if existing != candidateID {
		return ErrLiteratureConflict
	}
	return nil
}

func (s *Store) StageLiteratureCandidate(ctx context.Context, owner string, input LiteratureCandidateStage) (LiteratureCandidateReceipt, error) {
	if input.Source != nil && (!validLiteratureID(input.Source.ItemID) || input.Source.MetadataRevision < 1 || (input.Source.Direction != "references" && input.Source.Direction != "citations")) {
		return LiteratureCandidateReceipt{}, ErrLiteratureInvalid
	}
	if !candidateString(input.Provider, 64) || !candidateString(input.ExternalID, 512) || strings.TrimSpace(input.Title) == "" || len(input.Title) > 4096 {
		return LiteratureCandidateReceipt{}, ErrLiteratureInvalid
	}
	raw, err := literatureMetadata(input.Metadata)
	if err != nil {
		return LiteratureCandidateReceipt{}, err
	}
	meta, err := decodeLiteratureMetadataObject(raw)
	if err != nil {
		return LiteratureCandidateReceipt{}, err
	}
	ids, err := candidateIdentities(meta)
	if err != nil {
		return LiteratureCandidateReceipt{}, err
	}
	input.Metadata = meta
	return s.candidateMutation(ctx, owner, input.OperationID, "stage", input, func(tx *sql.Tx) (LiteratureCandidate, error) {
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
	})
}
func (s *Store) DecideLiteratureCandidate(ctx context.Context, owner, id, action string, input LiteratureCandidateAction) (LiteratureCandidateReceipt, error) {
	if !validLiteratureID(id) || (action != "accept" && action != "dismiss") {
		return LiteratureCandidateReceipt{}, ErrLiteratureInvalid
	}
	request := struct {
		ID    string                    `json:"id"`
		Input LiteratureCandidateAction `json:"input"`
	}{id, input}
	return s.candidateMutation(ctx, owner, input.OperationID, action, request, func(tx *sql.Tx) (LiteratureCandidate, error) {
		c, e := readLiteratureCandidate(ctx, tx, owner, id)
		if e != nil {
			return c, e
		}
		if c.State == "accepted" {
			if action == "dismiss" {
				return c, ErrLiteratureConflict
			}
			return c, requireCandidateAcceptedItem(ctx, tx, owner, c)
		}
		if action == "dismiss" {
			_, e = tx.ExecContext(ctx, `UPDATE literature_candidates SET state='dismissed',updated_at=? WHERE id=? AND owner_user_id=?`, s.now().UTC(), id, owner)
			if e != nil {
				return c, e
			}
			return readLiteratureCandidate(ctx, tx, owner, id)
		}
		ids, e := candidateIdentities(c.Metadata)
		if e != nil {
			return c, e
		}
		sources, e := readCandidateSources(ctx, tx, owner, c)
		if e != nil {
			return c, e
		}
		item, e := findMatchingLibraryItem(ctx, tx, owner, ids, sources...)
		if e != nil {
			return c, e
		}
		if item == "" {
			item = uuid.NewString()
			_, raw, e := candidateLibraryMetadata(c.Metadata, c.Provider, c.ExternalID)
			if e != nil {
				return c, e
			}
			now := s.now().UTC()
			if _, e = tx.ExecContext(ctx, `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,?,?,?,?,?)`, item, owner, c.Title, raw, now, now); e != nil {
				return c, e
			}
		}
		_, e = tx.ExecContext(ctx, `UPDATE literature_candidates SET state='accepted',item_id=?,updated_at=? WHERE id=? AND owner_user_id=?`, item, s.now().UTC(), id, owner)
		if e != nil {
			return c, e
		}
		return readLiteratureCandidate(ctx, tx, owner, id)
	})
}
