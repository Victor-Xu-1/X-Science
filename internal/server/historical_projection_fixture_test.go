package server

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Private historical fixtures must restore the old DDL as well as its journal
// prefix. Only the empty derived projection state is replaced; source events,
// Library records and compute jobs are retained for the original migrations.
func restoreEmptyHistoricalProjectionState(ctx context.Context, tx *sql.Tx, target int) error {
	var targetDDL string
	switch target {
	case 74:
		targetDDL = transcriptstore.WebReadModelV74ProjectionStateStatement()
	case 79:
		targetDDL = transcriptstore.WebReadModelV79ProjectionStateStatement()
	default:
		return fmt.Errorf("unreviewed historical projection target%d", target)
	}
	var facts int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM transcript_web_projection_state)+
		(SELECT COUNT(*) FROM transcript_web_messages)+(SELECT COUNT(*) FROM transcript_web_message_identities)+
		(SELECT COUNT(*) FROM transcript_web_message_artifact_refs)`).Scan(&facts); err != nil {
		return err
	}
	if facts != 0 {
		return fmt.Errorf("historical projection reconstruction requires empty private derived tables: facts=%d", facts)
	}
	var definition string
	if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name='transcript_web_projection_state'`).Scan(&definition); err != nil {
		return err
	}
	if strings.TrimSpace(definition) != strings.TrimSpace(transcriptstore.WebReadModelV93ProjectionStateStatement()) {
		return fmt.Errorf("historical projection reconstruction found unreviewed current DDL")
	}
	names, statements := transcriptstore.WebReadModelV38ObjectNames(), transcriptstore.WebReadModelV38CanonicalStatements()
	if len(names) != len(statements) {
		return fmt.Errorf("canonical historical projection schema is inconsistent")
	}
	var readyIndex string
	for index, name := range names {
		if name == "transcript_web_projection_ready" {
			readyIndex = statements[index]
		}
	}
	if readyIndex == "" {
		return fmt.Errorf("canonical historical projection ready index is missing")
	}
	for _, statement := range []string{`DROP TABLE transcript_web_projection_state`, targetDDL, readyIndex} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name='transcript_web_projection_state'`).Scan(&definition); err != nil {
		return err
	}
	if strings.TrimSpace(definition) != strings.TrimSpace(targetDDL) {
		return fmt.Errorf("historical projection target%d DDL was not restored", target)
	}
	return nil
}

func requireHistoricalFixturePrefix(ctx context.Context, tx *sql.Tx, target int) error {
	var count, version int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),MAX(version) FROM workspace_schema_migrations`).Scan(&count, &version); err != nil {
		return err
	}
	if count != target || version != target {
		return fmt.Errorf("historical fixture is not the complete%d prefix: count=%d max=%d", target, count, version)
	}
	return nil
}
