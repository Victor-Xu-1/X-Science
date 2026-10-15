package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

const (
	transcriptWebProjectorV79CallbackID        = "transcript-web-projector-v79-rebuild"
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
	var tableSQL string
	if err := executor.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema
		WHERE type='table' AND name='transcript_web_projection_state'`).Scan(&tableSQL); err != nil {
		return fmt.Errorf("inspect transcript Web projection predecessor: %w", err)
	}
	compact := strings.NewReplacer(" ", "", "\n", "", "\r", "", "\t", "")
	if compact.Replace(tableSQL) != compact.Replace(transcriptstore.WebReadModelV74ProjectionStateStatement()) {
		return errors.New("transcript Web projector v13 predecessor is required")
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
	for _, statement := range transcriptstore.WebReadModelProjectorV79RebuildStatements() {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
