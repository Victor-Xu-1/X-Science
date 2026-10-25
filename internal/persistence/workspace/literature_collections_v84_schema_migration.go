package workspace

import (
	"context"
	"errors"
)

const (
	literatureCollectionsV84CallbackID        = "literature-ordinary-collections-v84-noop"
	literatureCollectionsV84PreflightIdentity = "literature-ordinary-collections-v84-empty-target-v1"
	literatureCollectionsV84RuleSpec          = "x-science.workspace.literature-ordinary-collections.v84"
)

var literatureCollectionsV84Migration = versionedSchemaMigration{
	version: 84,
	name:    "literature-ordinary-collections",
	identityV2: &schemaMigrationIdentityV2{
		CallbackID:        literatureCollectionsV84CallbackID,
		PreflightIdentity: literatureCollectionsV84PreflightIdentity,
		RuleSpec:          literatureCollectionsV84RuleSpec,
	},
	statements: []string{
		`CREATE TABLE literature_collections (
 id TEXT PRIMARY KEY,owner_user_id TEXT NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1 CHECK(typeof(revision)='integer' AND revision>0),
 name TEXT NOT NULL CHECK(length(trim(name))>0),name_key TEXT NOT NULL CHECK(length(name_key)>0),
 description TEXT NOT NULL DEFAULT '',parent_id TEXT REFERENCES literature_collections(id) ON DELETE SET NULL,
 sort_order INTEGER NOT NULL DEFAULT 0 CHECK(typeof(sort_order)='integer' AND sort_order>=0),
 created_at TEXT NOT NULL,updated_at TEXT NOT NULL,CHECK(parent_id IS NULL OR parent_id<>id),
 UNIQUE(owner_user_id,parent_id,name_key))`,
		`CREATE UNIQUE INDEX literature_collections_root_name_idx ON literature_collections(owner_user_id,name_key) WHERE parent_id IS NULL`,
		`CREATE INDEX literature_collections_owner_parent_order_idx ON literature_collections(owner_user_id,parent_id,sort_order,id)`,
		`CREATE TABLE literature_collection_items (
 collection_id TEXT NOT NULL REFERENCES literature_collections(id) ON DELETE CASCADE,
 item_id TEXT NOT NULL REFERENCES literature_items(id) ON DELETE CASCADE,
 sort_order INTEGER NOT NULL CHECK(typeof(sort_order)='integer' AND sort_order>=0),
 added_at TEXT NOT NULL,PRIMARY KEY(collection_id,item_id))`,
		`CREATE INDEX literature_collection_items_item_idx ON literature_collection_items(item_id,collection_id)`,
		`CREATE INDEX literature_collection_items_order_idx ON literature_collection_items(collection_id,sort_order,item_id)`,
	},
}

func preflightLiteratureCollectionsV84(ctx context.Context, query schemaMigrationQueryExecutor) error {
	var dependencies, targets int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='literature_items'`).Scan(&dependencies); err != nil {
		return err
	}
	if dependencies != 1 {
		return errors.New("literature collection dependency is unavailable")
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name IN
 ('literature_collections','literature_collection_items','literature_collections_root_name_idx',
 'literature_collections_owner_parent_order_idx','literature_collection_items_item_idx','literature_collection_items_order_idx')`).Scan(&targets); err != nil {
		return err
	}
	if targets != 0 {
		return errors.New("literature collection migration target is not empty")
	}
	return nil
}
