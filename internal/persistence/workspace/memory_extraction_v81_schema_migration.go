package workspace

import (
	"context"
	"errors"
)

const (
	memoryExtractionV81CallbackID        = "memory-extraction-receipts-v81-noop"
	memoryExtractionV81PreflightIdentity = "memory-extraction-receipts-v81-empty-v1"
	memoryExtractionV81RuleSpec          = "x-science.workspace.memory-extraction-receipts.v81"
)

var memoryExtractionV81Migration = versionedSchemaMigration{
	version: 81,
	name:    "atomic-memory-extraction-receipts",
	identityV2: &schemaMigrationIdentityV2{
		CallbackID:        memoryExtractionV81CallbackID,
		PreflightIdentity: memoryExtractionV81PreflightIdentity,
		RuleSpec:          memoryExtractionV81RuleSpec,
	},
	statements: []string{
		`CREATE TABLE memory_extraction_receipts (
		 source_key TEXT PRIMARY KEY CHECK(length(source_key)=64),
		 owner_user_id TEXT NOT NULL,
		 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		 frame_id TEXT NOT NULL REFERENCES frames(id) ON DELETE CASCADE,
		 frame_incarnation_id TEXT NOT NULL,
		 source_json TEXT NOT NULL CHECK(json_valid(source_json)),
		 source_sha256 TEXT NOT NULL CHECK(length(source_sha256)=64),
		 input_sha256 TEXT NOT NULL CHECK(length(input_sha256)=64),
		 plan_sha256 TEXT NOT NULL CHECK(length(plan_sha256)=64),
		 result_json TEXT NOT NULL CHECK(json_valid(result_json)),
		 created_at TIMESTAMP NOT NULL)`,
		`CREATE INDEX memory_extraction_receipts_frame_idx ON memory_extraction_receipts(
		 frame_id,owner_user_id,frame_incarnation_id,
		 json_extract(source_json,'$.Transcript.StreamUID'),
		 json_extract(source_json,'$.Transcript.StreamEpoch'),
		 json_extract(source_json,'$.Transcript.PublicationSeq') DESC)`,
		`CREATE TRIGGER memory_extraction_receipts_immutable BEFORE UPDATE ON memory_extraction_receipts
		 BEGIN SELECT RAISE(ABORT,'memory extraction receipts are immutable'); END`,
	},
}

func preflightMemoryExtractionV81(ctx context.Context, query schemaMigrationQueryExecutor) error {
	var count int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema
		WHERE name IN ('memory_extraction_receipts','memory_extraction_receipts_frame_idx','memory_extraction_receipts_immutable')`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("memory extraction receipt migration target is not empty")
	}
	return nil
}
