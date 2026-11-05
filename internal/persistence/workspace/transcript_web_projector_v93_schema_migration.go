package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	transcriptstore "x-science/internal/persistence/transcript"
)

const (
	transcriptWebProjectorV93CallbackID        = "transcript-web-projector-v93-rebuild-preserve-external-schema-v1"
	transcriptWebProjectorV93PreflightIdentity = "transcript-web-projector-v93-upgrade-v1"
	transcriptWebProjectorV93RuleSpec          = "x-science.workspace.transcript-web-projector.v93"
)

var transcriptWebProjectorV93Migration = versionedSchemaMigration{
	version: 93, name: "transcript-web-projector-v15",
	identityV2: &schemaMigrationIdentityV2{CallbackID: transcriptWebProjectorV93CallbackID,
		PreflightIdentity: transcriptWebProjectorV93PreflightIdentity, RuleSpec: transcriptWebProjectorV93RuleSpec},
}

func transcriptWebProjectorV93SourceObjects() (map[string]string, error) {
	owned, err := transcriptWebProjectorV79SourceObjects()
	if err == nil {
		owned["transcript_web_projection_state"] = transcriptstore.WebReadModelV79ProjectionStateStatement()
	}
	return owned, err
}

func preflightTranscriptWebProjectorV93(ctx context.Context, query schemaMigrationQueryExecutor) error {
	owned, err := transcriptWebProjectorV93SourceObjects()
	if err != nil {
		return err
	}
	if err := inspectTranscriptWebProjectorSource(ctx, query, owned); err != nil {
		return err
	}
	var staging int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name IN (
		'transcript_web_projection_state_v92','transcript_web_messages_v92',
		'transcript_web_message_identities_v92','transcript_web_message_artifact_refs_v92')`).Scan(&staging); err != nil {
		return fmt.Errorf("inspect transcript Web projector staging: %w", err)
	}
	if staging != 0 {
		return errors.New("transcript Web projector v93 migration staging is polluted")
	}
	return nil
}

func migrateTranscriptWebProjectorV93(ctx context.Context, tx *sql.Tx) error {
	if err := preflightTranscriptWebProjectorV93(ctx, tx); err != nil {
		return err
	}
	owned, err := transcriptWebProjectorV93SourceObjects()
	if err != nil {
		return err
	}
	target := transcriptstore.WebReadModelV93ProjectionStateStatement()
	before, err := transcriptWebProjectorExternalSchema(ctx, tx, owned, target)
	if err != nil {
		return err
	}
	for _, statement := range transcriptstore.WebReadModelProjectorV93RebuildStatements() {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	after, err := transcriptWebProjectorExternalSchema(ctx, tx, owned, target)
	if err != nil {
		return err
	}
	return requireTranscriptWebProjectorExternalSchemaUnchanged(before, after)
}
