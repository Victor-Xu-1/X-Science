package workspace

import (
	"context"
	"errors"
)

const (
	literatureCandidatesV87CallbackID        = "literature-candidates-v87-noop"
	literatureCandidatesV87PreflightIdentity = "literature-candidates-v87-empty-target-v1"
	literatureCandidatesV87RuleSpec          = "x-science.workspace.literature-candidates.v87"
)

// Candidate metadata and decisions are prospective; migration never derives or
// changes existing Library rows, attachments, or mutation receipts.
var literatureCandidatesV87Migration = versionedSchemaMigration{
	version: 87,
	name:    "literature-candidates",
	identityV2: &schemaMigrationIdentityV2{
		CallbackID:        literatureCandidatesV87CallbackID,
		PreflightIdentity: literatureCandidatesV87PreflightIdentity,
		RuleSpec:          literatureCandidatesV87RuleSpec,
	},
	statements: []string{
		`CREATE TABLE literature_candidates (
 id TEXT PRIMARY KEY, owner_user_id TEXT NOT NULL,
 provider TEXT NOT NULL, external_id TEXT NOT NULL, title TEXT NOT NULL,
 metadata_json TEXT NOT NULL CHECK(json_valid(metadata_json)), doi_key TEXT,
 state TEXT NOT NULL CHECK(state IN ('pending','accepted','dismissed')),
 item_id TEXT REFERENCES literature_items(id), created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 CHECK((state='accepted' AND item_id IS NOT NULL) OR (state IN ('pending','dismissed') AND item_id IS NULL)),
 UNIQUE(owner_user_id,provider,external_id), UNIQUE(owner_user_id,id))`,
		`CREATE UNIQUE INDEX literature_candidates_owner_doi_idx ON literature_candidates(owner_user_id,doi_key) WHERE doi_key IS NOT NULL`,
		`CREATE INDEX literature_candidates_owner_state_order_idx ON literature_candidates(owner_user_id,state,id)`,
		`CREATE TABLE literature_candidate_sources (
 owner_user_id TEXT NOT NULL, provider TEXT NOT NULL, external_id TEXT NOT NULL,
 candidate_id TEXT NOT NULL,
 PRIMARY KEY(owner_user_id,provider,external_id),
 FOREIGN KEY(owner_user_id,candidate_id) REFERENCES literature_candidates(owner_user_id,id) ON DELETE CASCADE)`,
		`CREATE INDEX literature_candidate_sources_candidate_idx ON literature_candidate_sources(candidate_id)`,
	},
}

func preflightLiteratureCandidatesV87(ctx context.Context, query schemaMigrationQueryExecutor) error {
	var dependencies, targets int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='literature_items'`).Scan(&dependencies); err != nil {
		return err
	}
	if dependencies != 1 {
		return errors.New("literature candidates dependency is unavailable")
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name IN ('literature_candidates','literature_candidates_owner_doi_idx','literature_candidates_owner_state_order_idx','literature_candidate_sources','literature_candidate_sources_candidate_idx')`).Scan(&targets); err != nil {
		return err
	}
	if targets != 0 {
		return errors.New("literature candidates migration target is not empty")
	}
	return nil
}
