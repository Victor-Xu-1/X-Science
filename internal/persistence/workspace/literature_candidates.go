package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type LiteratureCandidate struct {
	ID          string                 `json:"id"`
	Title       string                 `json:"title"`
	Metadata    map[string]any         `json:"metadata"`
	Provider    string                 `json:"provider"`
	ExternalID  string                 `json:"external_id"`
	State       string                 `json:"state"`
	ItemID      string                 `json:"item_id,omitempty"`
	TaskOrigins []LiteratureTaskOrigin `json:"task_origins,omitempty"`
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
	if err == nil {
		c.TaskOrigins, err = readLiteratureTaskOrigins(ctx, q, owner, id)
	}
	return c, err
}

func requireCandidateAcceptedItem(ctx context.Context, tx workspaceTransaction, owner string, c LiteratureCandidate) error {
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
	if err = rows.Err(); err != nil {
		return page, err
	}
	if err = rows.Close(); err != nil {
		return page, err
	}
	for i := range page.Candidates {
		page.Candidates[i].TaskOrigins, err = readLiteratureTaskOrigins(ctx, s.readDatabase(), owner, page.Candidates[i].ID)
		if err != nil {
			return page, err
		}
	}
	return page, nil
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
func recordCandidateSource(ctx context.Context, tx workspaceTransaction, owner, provider, external, candidateID string) error {
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

func (s *Store) DecideLiteratureCandidate(ctx context.Context, owner, id, action string, input LiteratureCandidateAction) (LiteratureCandidateReceipt, error) {
	if !validLiteratureID(id) || (action != "accept" && action != "dismiss" && action != "restore") {
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
			if action != "accept" {
				return c, ErrLiteratureConflict
			}
			return c, requireCandidateAcceptedItem(ctx, tx, owner, c)
		}
		if action == "restore" {
			if c.State != "dismissed" {
				return c, ErrLiteratureConflict
			}
			result, err := tx.ExecContext(ctx, `UPDATE literature_candidates SET state='pending',updated_at=? WHERE id=? AND owner_user_id=? AND state='dismissed'`, s.now().UTC(), id, owner)
			if err != nil {
				return c, err
			}
			changed, err := result.RowsAffected()
			if err != nil {
				return c, err
			}
			if changed != 1 {
				return c, ErrLiteratureConflict
			}
			return readLiteratureCandidate(ctx, tx, owner, id)
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
		item, e := s.findOrCreateLiteratureMetadataTx(ctx, tx, owner, c.Title, func() (string, error) {
			_, raw, err := candidateLibraryMetadata(c.Metadata, c.Provider, c.ExternalID)
			return raw, err
		}, ids, sources...)
		if e != nil {
			return c, e
		}
		if e = s.linkLiteratureTaskDestinationsTx(ctx, tx, owner, id, item); e != nil {
			return c, e
		}
		_, e = tx.ExecContext(ctx, `UPDATE literature_candidates SET state='accepted',item_id=?,updated_at=? WHERE id=? AND owner_user_id=?`, item, s.now().UTC(), id, owner)
		if e != nil {
			return c, e
		}
		return readLiteratureCandidate(ctx, tx, owner, id)
	})
}
