package workspace

import (
	"context"
	"errors"
	transcriptstore "x-science/internal/persistence/transcript"
)

const (
	namespaceCutoverV75CallbackID        = "namespace-cutover-v75-capture-source-receipts"
	namespaceCutoverV75PreflightIdentity = "namespace-cutover-v75-receipts-v1"
	namespaceCutoverV75RuleSpec          = "x-science.workspace.namespace-cutover.v75"
)

// The frozen receipt inventory is the boundary for historical identity reads.
// It never converts user content, changes an effect's state, or creates a job.
var namespaceCutoverV75Migration = versionedSchemaMigration{
	version: 75,
	name:    "namespace-cutover-receipts",
	identityV2: &schemaMigrationIdentityV2{
		CallbackID:        namespaceCutoverV75CallbackID,
		PreflightIdentity: namespaceCutoverV75PreflightIdentity,
		RuleSpec:          namespaceCutoverV75RuleSpec,
	},
	statements: append([]string{
		`CREATE TABLE namespace_cutover_receipts (
			kind TEXT NOT NULL, existing_id TEXT NOT NULL,
			PRIMARY KEY(kind,existing_id)
		) STRICT`,
		`INSERT INTO namespace_cutover_receipts SELECT 'outbox',event_id FROM workspace_outbox`,
		`INSERT INTO namespace_cutover_receipts SELECT 'frame',id FROM frames`,
		`INSERT INTO namespace_cutover_receipts SELECT 'transcript-stream',stream_uid FROM transcript_streams`,
		`CREATE TABLE namespace_transcript_event_receipts (
			stream_uid TEXT NOT NULL, event_id INTEGER NOT NULL, source_sha256 TEXT NOT NULL,
			PRIMARY KEY(stream_uid,event_id)
		) STRICT`,
		`CREATE TABLE namespace_history_audit_receipts (
			run_id BLOB PRIMARY KEY NOT NULL, stream_uid TEXT NOT NULL, owner_id TEXT NOT NULL, source_sha256 TEXT NOT NULL
		) STRICT`,
		`INSERT INTO namespace_cutover_receipts SELECT 'outbox-realtime',json_extract(payload_json,'$.event.id')
			FROM workspace_outbox WHERE topic='workspace.realtime' AND json_valid(payload_json)
			AND json_type(payload_json,'$.event.id')='text' GROUP BY json_extract(payload_json,'$.event.id')`,
		`INSERT INTO namespace_cutover_receipts SELECT 'frame-event',id FROM frame_events`,
		`INSERT INTO namespace_cutover_receipts SELECT 'realtime-event',id FROM realtime_events`,
		`INSERT INTO namespace_cutover_receipts SELECT 'kernel-operation',operation_id FROM kernel_local_operations`,
		`INSERT INTO namespace_cutover_receipts SELECT 'tool-batch',batch_id FROM transcript_tool_call_batches`,
		`INSERT INTO namespace_cutover_receipts SELECT 'artifact',id FROM artifacts`,
		`INSERT INTO namespace_cutover_receipts SELECT 'artifact-version',id FROM artifact_versions`,
		`INSERT INTO namespace_cutover_receipts SELECT 'compute-job',job_id FROM compute_workbench_jobs`,
		`CREATE TABLE namespace_compute_job_receipts (
			job_id TEXT PRIMARY KEY NOT NULL, original_state TEXT NOT NULL, source_sha256 TEXT NOT NULL, original_raw_sha256 TEXT NOT NULL,
			original_external_id TEXT NOT NULL, original_output_manifest_sha256 TEXT NOT NULL, original_checkpoint_manifest_sha256 TEXT NOT NULL
		) STRICT`,
		`CREATE TABLE namespace_kernel_operation_receipts (
			operation_id TEXT PRIMARY KEY NOT NULL, original_state TEXT NOT NULL,
			execution_id TEXT NOT NULL, input_sha256 TEXT NOT NULL, environment TEXT NOT NULL,
			kernel_id TEXT NOT NULL, kernel_generation INTEGER NOT NULL, started_at TEXT NOT NULL
		) STRICT`,
		`CREATE TABLE compute_namespace_observed_source_receipts (
			job_id TEXT NOT NULL, owner_user_id TEXT NOT NULL, source_kind TEXT NOT NULL CHECK(source_kind='checkpoint'),
			source_sha256 TEXT NOT NULL,input_sha256 TEXT NOT NULL,environment TEXT NOT NULL,provider TEXT NOT NULL,external_id TEXT NOT NULL,
			manifest_sha256 TEXT NOT NULL,created_at_ms INTEGER NOT NULL,
			PRIMARY KEY(job_id,source_kind,manifest_sha256),
			FOREIGN KEY(job_id) REFERENCES compute_workbench_jobs(job_id) ON DELETE CASCADE
		) STRICT`,
		`CREATE TRIGGER compute_namespace_observed_source_receipts_update_guard BEFORE UPDATE ON compute_namespace_observed_source_receipts
		BEGIN SELECT RAISE(ABORT,'compute observed source receipt is immutable'); END`,
		`CREATE TRIGGER compute_namespace_observed_source_receipts_delete_guard BEFORE DELETE ON compute_namespace_observed_source_receipts
		WHEN EXISTS(SELECT 1 FROM compute_workbench_jobs WHERE job_id=OLD.job_id)
		BEGIN SELECT RAISE(ABORT,'compute observed source receipt is immutable'); END`,
		`INSERT INTO namespace_kernel_operation_receipts SELECT operation_id,state,COALESCE(execution_id,''),
			input_sha256,environment,COALESCE(kernel_id,''),COALESCE(kernel_generation,0),COALESCE(CAST(started_at AS TEXT),'')
			FROM kernel_local_operations`,
		`CREATE TRIGGER namespace_kernel_operation_receipts_insert_guard BEFORE INSERT ON namespace_kernel_operation_receipts
		BEGIN SELECT RAISE(ABORT,'kernel namespace cutover inventory is sealed'); END`,
		`CREATE TRIGGER namespace_kernel_operation_receipts_update_guard BEFORE UPDATE ON namespace_kernel_operation_receipts
		BEGIN SELECT RAISE(ABORT,'kernel namespace cutover inventory is immutable'); END`,
		`CREATE TRIGGER namespace_kernel_operation_receipts_delete_guard BEFORE DELETE ON namespace_kernel_operation_receipts
		BEGIN SELECT RAISE(ABORT,'kernel namespace cutover inventory is immutable'); END`,
		`CREATE TABLE namespace_identity_aliases (
			kind TEXT NOT NULL, canonical_id TEXT NOT NULL, existing_id TEXT NOT NULL,
			created_at_ms INTEGER NOT NULL,
			PRIMARY KEY(kind,canonical_id), UNIQUE(kind,existing_id),
			FOREIGN KEY(kind,existing_id) REFERENCES namespace_cutover_receipts(kind,existing_id)
		) STRICT`,
		`CREATE TRIGGER namespace_cutover_receipts_insert_guard BEFORE INSERT ON namespace_cutover_receipts
		BEGIN SELECT RAISE(ABORT,'namespace cutover receipt inventory is sealed'); END`,
		`CREATE TRIGGER namespace_cutover_receipts_update_guard BEFORE UPDATE ON namespace_cutover_receipts
		BEGIN SELECT RAISE(ABORT,'namespace cutover receipt inventory is immutable'); END`,
		`CREATE TRIGGER namespace_cutover_receipts_delete_guard BEFORE DELETE ON namespace_cutover_receipts
		BEGIN SELECT RAISE(ABORT,'namespace cutover receipt inventory is immutable'); END`,
		`CREATE TRIGGER namespace_identity_aliases_update_guard BEFORE UPDATE ON namespace_identity_aliases
		BEGIN SELECT RAISE(ABORT,'namespace identity alias is immutable'); END`,
		`CREATE TRIGGER namespace_identity_aliases_delete_guard BEFORE DELETE ON namespace_identity_aliases
		BEGIN SELECT RAISE(ABORT,'namespace identity alias is immutable'); END`,
		`CREATE TABLE kernel_runtime_identity_receipts (
			operation_id TEXT PRIMARY KEY NOT NULL,
			execution_id TEXT NOT NULL, input_sha256 TEXT NOT NULL,
			runtime_kind TEXT NOT NULL, runtime_id TEXT NOT NULL,
			environment TEXT NOT NULL, environment_generation TEXT NOT NULL,
			image_id TEXT NOT NULL, namespace_origin TEXT NOT NULL CHECK(namespace_origin IN ('x-science','pre-cutover')), created_at_ms INTEGER NOT NULL,
			FOREIGN KEY(operation_id) REFERENCES kernel_local_operations(operation_id) ON DELETE CASCADE,
			UNIQUE(runtime_kind,runtime_id)
		) STRICT`,
		`CREATE TRIGGER kernel_runtime_identity_receipts_update_guard BEFORE UPDATE ON kernel_runtime_identity_receipts
		BEGIN SELECT RAISE(ABORT,'kernel runtime identity receipt is immutable'); END`,
		`CREATE TRIGGER kernel_runtime_identity_receipts_delete_guard BEFORE DELETE ON kernel_runtime_identity_receipts
		WHEN EXISTS(SELECT 1 FROM kernel_local_operations WHERE operation_id=OLD.operation_id)
		BEGIN SELECT RAISE(ABORT,'kernel runtime identity receipt is immutable'); END`,
	}, transcriptstore.WebReadModelProjectorV74RebuildStatements()...),
}

func preflightNamespaceCutoverV75(ctx context.Context, executor schemaMigrationQueryExecutor) error {
	var objects int
	if err := executor.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema
		WHERE name IN ('namespace_cutover_receipts','namespace_identity_aliases','kernel_runtime_identity_receipts','namespace_kernel_operation_receipts','namespace_transcript_event_receipts','namespace_compute_job_receipts','namespace_history_audit_receipts','compute_namespace_observed_source_receipts')`).Scan(&objects); err != nil {
		return err
	}
	if objects != 0 {
		return errors.New("namespace cutover staging is polluted")
	}
	return nil
}
