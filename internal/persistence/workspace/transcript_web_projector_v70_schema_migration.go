package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	transcriptstore "synon-go/internal/persistence/transcript"
)

const (
	transcriptWebProjectorV70CallbackID        = "transcript-web-projector-v70-rebuild"
	transcriptWebProjectorV70PreflightIdentity = "transcript-web-projector-v70-upgrade-v1"
	transcriptWebProjectorV70RuleSpec          = "synon.workspace.transcript-web-projector.v70"
)

var transcriptWebProjectorV70Migration = versionedSchemaMigration{
	version: 70,
	name:    "transcript-web-projector-v12",
	identityV2: &schemaMigrationIdentityV2{
		CallbackID:        transcriptWebProjectorV70CallbackID,
		RuleSpec:          transcriptWebProjectorV70RuleSpec,
		PreflightIdentity: transcriptWebProjectorV70PreflightIdentity,
	},
}

func preflightTranscriptWebProjectorV70(ctx context.Context, executor schemaMigrationQueryExecutor) error {
	var tableSQL string
	if err := executor.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema
		WHERE type='table' AND name='transcript_web_projection_state'`).Scan(&tableSQL); err != nil {
		return errors.New("inspect transcript Web projection state")
	}
	compact := strings.NewReplacer(" ", "", "\n", "", "\r", "", "\t", "").Replace(tableSQL)
	if !strings.Contains(compact, "CHECK(projector_versionIN(1,2,3,4,5,6,7,8,9,10,11))") {
		return errors.New("transcript Web projector v67 version constraint is required")
	}
	for _, staging := range []string{
		"transcript_web_projection_state_v69", "transcript_web_messages_v69",
		"transcript_web_message_identities_v69", "transcript_web_message_artifact_refs_v69",
	} {
		var count int
		if err := executor.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name=?`, staging).Scan(&count); err != nil || count != 0 {
			return errors.New("transcript Web projector v70 migration staging is polluted")
		}
	}
	return nil
}

func migrateTranscriptWebProjectorV70(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range transcriptstore.WebReadModelProjectorV70RebuildStatements() {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
