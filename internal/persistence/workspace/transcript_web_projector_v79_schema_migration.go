package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	transcriptstore "x-science/internal/persistence/transcript"
)

const (
	transcriptWebProjectorV79CallbackID        = "transcript-web-projector-v79-rebuild-preserve-external-schema-v2"
	transcriptWebProjectorV79PreflightIdentity = "transcript-web-projector-v79-upgrade-v1"
	transcriptWebProjectorV79RuleSpec          = "x-science.workspace.transcript-web-projector.v79"
)

var transcriptWebProjectorV79Migration = versionedSchemaMigration{
	version: 79,
	name:    "transcript-web-projector-v14",
	identityV2: &schemaMigrationIdentityV2{
		CallbackID:        transcriptWebProjectorV79CallbackID,
		RuleSpec:          transcriptWebProjectorV79RuleSpec,
		PreflightIdentity: transcriptWebProjectorV79PreflightIdentity,
	},
}

func preflightTranscriptWebProjectorV79(ctx context.Context, executor schemaMigrationQueryExecutor) error {
	if err := inspectTranscriptWebProjectorV79Source(ctx, executor); err != nil {
		return err
	}
	var staging int
	if err := executor.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name IN (
		'transcript_web_projection_state_v78','transcript_web_messages_v78',
		'transcript_web_message_identities_v78','transcript_web_message_artifact_refs_v78')`).Scan(&staging); err != nil {
		return fmt.Errorf("inspect transcript Web projector staging: %w", err)
	}
	if staging != 0 {
		return errors.New("transcript Web projector v79 migration staging is polluted")
	}
	return nil
}

func migrateTranscriptWebProjectorV79(ctx context.Context, tx *sql.Tx) error {
	// Recheck the same complete source contract in the DDL transaction; an
	// earlier preflight read is not authority over a later schema snapshot.
	if err := preflightTranscriptWebProjectorV79(ctx, tx); err != nil {
		return err
	}
	external, err := transcriptWebProjectorV79ExternalSchema(ctx, tx)
	if err != nil {
		return err
	}
	for _, statement := range transcriptstore.WebReadModelProjectorV79RebuildStatements() {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	// An inbound reference outside the replaced tables may have been rewritten
	// to a staging name. Reject within this same transaction so all DDL, rows
	// and the not-yet-written journal entry roll back together.
	return preserveTranscriptWebProjectorV79ExternalSchema(ctx, tx, external)
}
