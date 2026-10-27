package workspace

import (
	"context"
	"errors"
)

// Reading state has its own column and leaves bibliographic metadata intact.
const (
	literatureReadingStatusV86CallbackID        = "literature-reading-status-v86-noop"
	literatureReadingStatusV86PreflightIdentity = "literature-reading-status-v86-empty-column-v1"
	literatureReadingStatusV86RuleSpec          = "x-science.workspace.literature-reading-status.v86"
)

var literatureReadingStatusV86Migration = versionedSchemaMigration{
	version:    86,
	name:       "literature-reading-status",
	identityV2: &schemaMigrationIdentityV2{CallbackID: literatureReadingStatusV86CallbackID, PreflightIdentity: literatureReadingStatusV86PreflightIdentity, RuleSpec: literatureReadingStatusV86RuleSpec},
	statements: []string{`ALTER TABLE literature_items ADD COLUMN reading_status TEXT NOT NULL DEFAULT 'unread' CHECK(reading_status IN ('unread','reading','read'))`},
}

func preflightLiteratureReadingStatusV86(ctx context.Context, query schemaMigrationQueryExecutor) error {
	var table, column int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='literature_items'`).Scan(&table); err != nil {
		return err
	}
	if table != 1 {
		return errors.New("literature reading status dependency is unavailable")
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('literature_items') WHERE name='reading_status'`).Scan(&column); err != nil {
		return err
	}
	if column != 0 {
		return errors.New("literature reading status column already exists")
	}
	return nil
}
