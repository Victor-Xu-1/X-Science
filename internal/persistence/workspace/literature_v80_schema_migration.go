package workspace

import (
	"context"
	"errors"
)

const (
	literatureV80CallbackID        = "independent-literature-library-v80-noop"
	literatureV80PreflightIdentity = "independent-literature-library-v80-predecessor-v1"
	literatureV80RuleSpec          = "x-science.workspace.independent-literature-library.v80"
)

var literatureV80Migration = versionedSchemaMigration{
	version:    80,
	name:       "independent-literature-library",
	identityV2: &schemaMigrationIdentityV2{CallbackID: literatureV80CallbackID, PreflightIdentity: literatureV80PreflightIdentity, RuleSpec: literatureV80RuleSpec},
	statements: []string{
		`CREATE TABLE literature_items (
		 id TEXT PRIMARY KEY, owner_user_id TEXT NOT NULL, title TEXT NOT NULL,
		 metadata_revision INTEGER NOT NULL DEFAULT 1 CHECK(metadata_revision>0),
		 metadata_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(metadata_json)),
		 deleted_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE INDEX literature_items_owner_idx ON literature_items(owner_user_id,id)`,
		`CREATE TABLE literature_attachments (
		 id TEXT PRIMARY KEY, item_id TEXT NOT NULL REFERENCES literature_items(id) ON DELETE RESTRICT,
		 kind TEXT NOT NULL CHECK(kind IN ('fullText','supporting')),
		 current_version_number INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL)`,
		`CREATE TABLE literature_attachment_versions (
		 id TEXT PRIMARY KEY, attachment_id TEXT NOT NULL REFERENCES literature_attachments(id) ON DELETE RESTRICT,
		 version_number INTEGER NOT NULL CHECK(version_number>0), filename TEXT NOT NULL, mime_type TEXT NOT NULL,
		 size_bytes INTEGER NOT NULL CHECK(size_bytes>0), checksum TEXT NOT NULL,
		 page_count INTEGER NOT NULL CHECK(page_count>0), storage_path TEXT NOT NULL UNIQUE,
		 content_available INTEGER NOT NULL DEFAULT 1 CHECK(content_available IN (0,1)),
		 origin_version_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
		 UNIQUE(attachment_id,version_number))`,
		`CREATE INDEX literature_attachment_item_idx ON literature_attachments(item_id,id)`,
		`CREATE TABLE project_literature (
		 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		 item_id TEXT NOT NULL REFERENCES literature_items(id) ON DELETE RESTRICT,
		 added_at TEXT NOT NULL, PRIMARY KEY(project_id,item_id))`,
	},
}

func preflightLiteratureV80(ctx context.Context, query schemaMigrationQueryExecutor) error {
	var count int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name IN('literature_items','literature_attachments','literature_attachment_versions','project_literature')`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("independent literature migration target is not empty")
	}
	return nil
}
