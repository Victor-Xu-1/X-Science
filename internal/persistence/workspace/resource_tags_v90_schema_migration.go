package workspace

import (
	"context"
	"errors"
)

const (
	resourceTagsV90CallbackID        = "resource-tags-v90-noop"
	resourceTagsV90PreflightIdentity = "resource-tags-v90-empty-target-v1"
	resourceTagsV90RuleSpec          = "x-science.workspace.resource-tags.v90"
)

var resourceTagsV90Migration = versionedSchemaMigration{
	version: 90, name: "resource-tags",
	identityV2: &schemaMigrationIdentityV2{CallbackID: resourceTagsV90CallbackID, PreflightIdentity: resourceTagsV90PreflightIdentity, RuleSpec: resourceTagsV90RuleSpec},
	statements: []string{
		`CREATE TABLE resource_tag_catalog_state (
 owner_user_id TEXT PRIMARY KEY, revision INTEGER NOT NULL DEFAULT 0 CHECK(typeof(revision)='integer' AND revision>=0))`,
		`CREATE TABLE resource_tags (
 owner_user_id TEXT NOT NULL, id TEXT NOT NULL, system_key TEXT, name TEXT, name_key TEXT, icon_key TEXT, color_key TEXT,
 sort_order INTEGER NOT NULL CHECK(typeof(sort_order)='integer' AND sort_order>=0),
 created_at INTEGER NOT NULL CHECK(typeof(created_at)='integer' AND created_at>0 AND created_at<=8640000000000000),
 updated_at INTEGER NOT NULL CHECK(typeof(updated_at)='integer' AND updated_at>0 AND updated_at<=8640000000000000),
 PRIMARY KEY(owner_user_id,id), UNIQUE(owner_user_id,system_key), UNIQUE(owner_user_id,name_key), UNIQUE(owner_user_id,sort_order),
 FOREIGN KEY(owner_user_id) REFERENCES resource_tag_catalog_state(owner_user_id) ON DELETE CASCADE,
 CHECK((system_key IS NOT NULL AND system_key='favorite' AND id='tag-favorite' AND name IS NULL AND name_key IS NULL AND icon_key IS NULL AND color_key IS NULL AND sort_order=0) OR
 (system_key IS NULL AND id<>'tag-favorite' AND name IS NOT NULL AND length(trim(name)) BETWEEN 1 AND 64 AND name_key IS NOT NULL AND length(name_key)>0 AND
 icon_key IS NOT NULL AND icon_key IN ('tag','star','bookmark','flask-conical','book-open','database','code-2','bot') AND
 color_key IS NOT NULL AND color_key IN ('gray','red','orange','amber','green','blue','purple','pink') AND sort_order>0)))`,
		`CREATE INDEX resource_tags_owner_order_idx ON resource_tags(owner_user_id,sort_order,id)`,
		`CREATE TABLE resource_tag_assignments (
 owner_user_id TEXT NOT NULL, tag_id TEXT NOT NULL, resource_type TEXT NOT NULL CHECK(length(trim(resource_type))>0),
 resource_id TEXT NOT NULL CHECK(length(trim(resource_id)) BETWEEN 1 AND 256),
 created_at INTEGER NOT NULL CHECK(typeof(created_at)='integer' AND created_at>0 AND created_at<=8640000000000000),
 PRIMARY KEY(owner_user_id,tag_id,resource_type,resource_id),
 FOREIGN KEY(owner_user_id,tag_id) REFERENCES resource_tags(owner_user_id,id) ON DELETE CASCADE)`,
		`CREATE INDEX resource_tag_assignments_resource_idx ON resource_tag_assignments(owner_user_id,resource_type,resource_id,tag_id)`,
		// Only a confirmed canonical deletion removes relationships. Trash,
		// temporary catalog failures and unavailable content retain them.
		`CREATE TRIGGER resource_tags_literature_delete AFTER DELETE ON literature_items BEGIN
 UPDATE resource_tag_catalog_state SET revision=revision+1 WHERE owner_user_id=OLD.owner_user_id AND EXISTS
 (SELECT 1 FROM resource_tag_assignments WHERE owner_user_id=OLD.owner_user_id AND resource_type='literature.item' AND resource_id=OLD.id);
 DELETE FROM resource_tag_assignments WHERE owner_user_id=OLD.owner_user_id AND resource_type='literature.item' AND resource_id=OLD.id;
 END`,
	},
}

func preflightResourceTagsV90(ctx context.Context, query schemaMigrationQueryExecutor) error {
	var dependencies, targets int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='literature_items'`).Scan(&dependencies); err != nil {
		return err
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name IN ('resource_tag_catalog_state','resource_tags','resource_tags_owner_order_idx','resource_tag_assignments','resource_tag_assignments_resource_idx','resource_tags_literature_delete')`).Scan(&targets); err != nil {
		return err
	}
	if dependencies != 1 || targets != 0 {
		return errors.New("resource tag migration requires original dependencies and empty targets")
	}
	return nil
}
