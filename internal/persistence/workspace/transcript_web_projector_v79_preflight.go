package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Only these objects are replaced by the established four-table rebuild.
// Their definitions come from the canonical Transcript authority, not copies
// here. Unknown extensions must remain untouched rather than be discarded.
func transcriptWebProjectorV79SourceObjects() (map[string]string, error) {
	replaced := map[string]bool{
		"transcript_web_projection_state":                  true,
		"transcript_web_messages":                          true,
		"transcript_web_message_identities":                true,
		"transcript_web_message_artifact_refs":             true,
		"transcript_web_message_identities_ordinal":        true,
		"transcript_web_message_artifact_refs_pair":        true,
		"transcript_web_messages_visible_index":            true,
		"transcript_web_messages_page":                     true,
		"transcript_web_messages_publication":              true,
		"transcript_web_projection_ready":                  true,
		"transcript_artifact_references_projection_retire": true,
	}
	names := transcriptstore.WebReadModelV38ObjectNames()
	statements := transcriptstore.WebReadModelV38CanonicalStatements()
	if len(names) != len(statements) {
		return nil, errors.New("transcript Web canonical object authority is inconsistent")
	}
	expected := make(map[string]string, len(replaced))
	for index, name := range names {
		if !replaced[name] {
			continue
		}
		statement := statements[index]
		if name == "transcript_web_projection_state" {
			statement = transcriptstore.WebReadModelV74ProjectionStateStatement()
		}
		expected[name] = strings.TrimSpace(statement)
	}
	if len(expected) != len(replaced) {
		return nil, errors.New("transcript Web canonical replacement objects are incomplete")
	}
	return expected, nil
}

func inspectTranscriptWebProjectorV79Source(ctx context.Context, executor schemaMigrationQueryExecutor) error {
	expected, err := transcriptWebProjectorV79SourceObjects()
	if err != nil {
		return err
	}
	rows, err := executor.QueryContext(ctx, `SELECT type,name,sql FROM sqlite_schema
		WHERE tbl_name IN ('transcript_web_projection_state','transcript_web_messages',
			'transcript_web_message_identities','transcript_web_message_artifact_refs')
			OR name='transcript_artifact_references_projection_retire' ORDER BY type,name`)
	if err != nil {
		return fmt.Errorf("inspect transcript Web predecessor objects: %w", err)
	}
	defer rows.Close()
	seen := make(map[string]bool, len(expected))
	for rows.Next() {
		var kind, name string
		var statement sql.NullString
		if err := rows.Scan(&kind, &name, &statement); err != nil {
			return fmt.Errorf("read transcript Web predecessor object: %w", err)
		}
		if kind == "index" && !statement.Valid && strings.HasPrefix(name, "sqlite_autoindex_") {
			continue // SQLite owns indexes implied by the exact table definitions.
		}
		canonical, known := expected[name]
		if !known || !statement.Valid || strings.TrimSpace(statement.String) != canonical || seen[name] {
			return fmt.Errorf("noncanonical transcript Web predecessor object %q", name)
		}
		seen[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate transcript Web predecessor objects: %w", err)
	}
	if len(seen) != len(expected) {
		return errors.New("transcript Web predecessor objects are incomplete")
	}
	return nil
}
