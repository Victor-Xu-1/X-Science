package workspace

import (
	"context"
	"errors"
)

const (
	libraryAnnotationV89CallbackID        = "library-annotation-owner-v89-noop"
	libraryAnnotationV89PreflightIdentity = "library-annotation-owner-v89-project-table-v1"
	libraryAnnotationV89RuleSpec          = "x-science.workspace.library-annotation-owner.v89"
)

// Library notes belong to the Library owner, not to the import's old project.
// The original table, row identities and project-cascade semantics are retained.
var libraryAnnotationV89Migration = versionedSchemaMigration{
	version: 89, name: "library-annotation-owner",
	identityV2: &schemaMigrationIdentityV2{CallbackID: libraryAnnotationV89CallbackID, PreflightIdentity: libraryAnnotationV89PreflightIdentity, RuleSpec: libraryAnnotationV89RuleSpec},
	statements: []string{
		`CREATE TABLE annotations_v89 (
 id TEXT PRIMARY KEY, project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
 library_owner_id TEXT, target_kind TEXT NOT NULL, target_key TEXT NOT NULL,
 label_idx INTEGER NOT NULL, content_checksum TEXT, body TEXT NOT NULL,
 created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP,
 CHECK ((target_kind='literature-attachment-version' AND project_id IS NULL AND library_owner_id IS NOT NULL AND length(library_owner_id)>0 AND target_key GLOB 'litv:litv_*') OR
 (target_kind<>'literature-attachment-version' AND project_id IS NOT NULL AND library_owner_id IS NULL)))`,
		`INSERT INTO annotations_v89(id,project_id,target_kind,target_key,label_idx,content_checksum,body,created_at,updated_at)
 SELECT id,project_id,target_kind,target_key,label_idx,content_checksum,body,created_at,updated_at FROM annotations`,
		`DROP TABLE annotations`,
		`ALTER TABLE annotations_v89 RENAME TO annotations`,
		`CREATE INDEX annotations_project_target_idx ON annotations(project_id,target_key,label_idx)`,
		`CREATE UNIQUE INDEX annotations_target_label_unique_idx ON annotations(project_id,target_key,label_idx)`,
		`CREATE UNIQUE INDEX annotations_library_target_label_unique_idx ON annotations(library_owner_id,target_key,label_idx) WHERE library_owner_id IS NOT NULL`,
	},
}

func preflightLibraryAnnotationV89(ctx context.Context, query schemaMigrationQueryExecutor) error {
	var columns, total, temporary, foreign, indexes, expectedIndexes int
	var shape, projectIndex, uniqueIndex, projectForeignKey string
	var projectKeys, uniqueKeys, outboundKeys int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('annotations') WHERE name IN ('id','project_id','target_kind','target_key','label_idx','content_checksum','body','created_at','updated_at')`).Scan(&columns); err != nil {
		return err
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name='annotations_v89' OR (type='trigger' AND tbl_name='annotations')`).Scan(&temporary); err != nil {
		return err
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema s JOIN pragma_foreign_key_list(s.name) f WHERE s.type='table' AND f."table"='annotations'`).Scan(&foreign); err != nil {
		return err
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('annotations')`).Scan(&total); err != nil {
		return err
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE tbl_name='annotations' AND type='index' AND name NOT IN ('sqlite_autoindex_annotations_1','annotations_project_target_idx','annotations_target_label_unique_idx')`).Scan(&indexes); err != nil {
		return err
	}
	if err := query.QueryRowContext(ctx, `SELECT group_concat(name||':'||upper(type)||':'||"notnull"||':'||pk,'|') FROM pragma_table_info('annotations')`).Scan(&shape); err != nil {
		return err
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_index_list('annotations') WHERE (name='annotations_project_target_idx' AND "unique"=0 AND partial=0) OR (name='annotations_target_label_unique_idx' AND "unique"=1 AND partial=0) OR (name='sqlite_autoindex_annotations_1' AND "unique"=1 AND origin='pk')`).Scan(&expectedIndexes); err != nil {
		return err
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*),group_concat(name||':'||desc||':'||coll,'|') FROM pragma_index_xinfo('annotations_project_target_idx') WHERE key=1`).Scan(&projectKeys, &projectIndex); err != nil {
		return err
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*),group_concat(name||':'||desc||':'||coll,'|') FROM pragma_index_xinfo('annotations_target_label_unique_idx') WHERE key=1`).Scan(&uniqueKeys, &uniqueIndex); err != nil {
		return err
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*),group_concat("from"||':'||"table"||':'||"to"||':'||on_update||':'||on_delete||':'||match,'|') FROM pragma_foreign_key_list('annotations')`).Scan(&outboundKeys, &projectForeignKey); err != nil {
		return err
	}
	if columns != 9 || total != 9 || indexes != 0 || expectedIndexes != 3 || projectKeys != 3 || uniqueKeys != 3 || outboundKeys != 1 || projectIndex != "project_id:0:BINARY|target_key:0:BINARY|label_idx:0:BINARY" || uniqueIndex != "project_id:0:BINARY|target_key:0:BINARY|label_idx:0:BINARY" || projectForeignKey != "project_id:projects:id:NO ACTION:CASCADE:NONE" || shape != "id:TEXT:0:1|project_id:TEXT:1:0|target_kind:TEXT:1:0|target_key:TEXT:1:0|label_idx:INTEGER:1:0|content_checksum:TEXT:0:0|body:TEXT:1:0|created_at:TIMESTAMP:1:0|updated_at:TIMESTAMP:0:0" || temporary != 0 || foreign != 0 {
		return errors.New("annotation predecessor shape is not the reviewed project table")
	}
	return nil
}
