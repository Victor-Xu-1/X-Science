package workspace

import (
	"context"
	"fmt"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

type transcriptWebProjectorV79ObjectKey struct {
	kind, name string
}

type transcriptWebProjectorV79ExternalObject struct {
	table, statement string
}

// SQLite itself resolves and rewrites inbound references during ALTER TABLE.
// Observe its real effect instead of guessing dependencies with SQL substrings:
// every non-owned definition must remain byte-identical before commit. This
// includes views, triggers on other tables, and inbound foreign-key definitions.
func transcriptWebProjectorV79ExternalSchema(ctx context.Context, executor schemaMigrationQueryExecutor) (map[transcriptWebProjectorV79ObjectKey]transcriptWebProjectorV79ExternalObject, error) {
	owned, err := transcriptWebProjectorV79SourceObjects()
	if err != nil {
		return nil, err
	}
	rows, err := executor.QueryContext(ctx, `SELECT type,name,tbl_name,sql FROM sqlite_schema
		WHERE sql IS NOT NULL ORDER BY type,name`)
	if err != nil {
		return nil, fmt.Errorf("inspect transcript Web external schema: %w", err)
	}
	defer rows.Close()
	objects := make(map[transcriptWebProjectorV79ObjectKey]transcriptWebProjectorV79ExternalObject)
	for rows.Next() {
		var key transcriptWebProjectorV79ObjectKey
		var object transcriptWebProjectorV79ExternalObject
		if err := rows.Scan(&key.kind, &key.name, &object.table, &object.statement); err != nil {
			return nil, fmt.Errorf("read transcript Web external schema: %w", err)
		}
		canonical, replaced := owned[key.name]
		statement := strings.TrimSpace(object.statement)
		if replaced && (statement == canonical ||
			(key.name == "transcript_web_projection_state" && statement == strings.TrimSpace(transcriptstore.WebReadModelV79ProjectionStateStatement()))) {
			continue
		}
		// Trigger names have their own namespace; a trigger may legitimately
		// share a canonical table name without being owned by this migration.
		objects[key] = object
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transcript Web external schema: %w", err)
	}
	return objects, nil
}

func preserveTranscriptWebProjectorV79ExternalSchema(ctx context.Context, executor schemaMigrationQueryExecutor, before map[transcriptWebProjectorV79ObjectKey]transcriptWebProjectorV79ExternalObject) error {
	after, err := transcriptWebProjectorV79ExternalSchema(ctx, executor)
	if err != nil {
		return err
	}
	for key, object := range before {
		if current, found := after[key]; !found || current != object {
			return fmt.Errorf("transcript Web projector migration would modify external %s %q", key.kind, key.name)
		}
	}
	if len(before) != len(after) {
		return fmt.Errorf("transcript Web projector migration would add external schema objects")
	}
	return nil
}
