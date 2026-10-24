package workspace

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

func (s *Store) ImportLiteratureArtifact(ctx context.Context, owner string, input LiteratureImport) (LiteratureReceipt, error) {
	var receipt LiteratureReceipt
	if !validLiteratureID(owner) || !validLiteratureID(input.OperationID) || !validLiteratureID(input.ArtifactID) || !validLiteratureID(input.VersionID) || strings.TrimSpace(input.Title) == "" || len(input.Title) > 4096 {
		return receipt, ErrLiteratureInvalid
	}
	metadata, err := literatureMetadata(input.Metadata)
	if err != nil {
		return receipt, err
	}
	// Detach caller-owned metadata before storage work and retain valid numeric
	// tokens in the first ACK, not only in the persisted metadata JSON.
	frozenMetadata, err := decodeLiteratureMetadataObject(metadata)
	if err != nil {
		return receipt, err
	}
	// Inspection facts are server-derived; request identity is the caller's
	// immutable source tuple/metadata, so retry survives source project deletion.
	identity, err := literatureImportRequestIdentity(input, metadata)
	if err != nil {
		return receipt, err
	}
	if found, err := s.lookupLiteratureMutationResult(ctx, owner, input.OperationID, "literature.import", identity, &receipt); err != nil {
		return receipt, err
	} else if found {
		return receipt, s.recoverLiteratureImportContent(ctx, owner, receipt.VersionID)
	}
	if input.PageCount < 1 {
		return receipt, ErrLiteratureInvalid
	}
	artifact, source, reader, found, err := s.OpenArtifactVersionContentForOwner(input.VersionID, owner)
	if err != nil {
		return receipt, err
	}
	if !found || artifact.ID != input.ArtifactID {
		if reader != nil {
			_ = reader.Close()
		}
		return receipt, ErrLiteratureUnavailable
	}
	defer reader.Close()
	staging, size, digest, err := s.stageArtifactWrite(ctx, reader, 50<<20)
	if err != nil {
		return receipt, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(staging)
		}
	}()
	if size != source.SizeBytes || digest != strings.ToLower(source.ContentSHA256) {
		return receipt, ErrLiteratureUnavailable
	}
	staged, err := openRegularFile(staging)
	if err != nil {
		return receipt, err
	}
	var header [5]byte
	_, headerErr := io.ReadFull(staged, header[:])
	_ = staged.Close()
	if headerErr != nil || string(header[:]) != "%PDF-" {
		return receipt, ErrLiteratureInvalid
	}
	itemID, attachmentID, versionID := uuid.NewString(), uuid.NewString(), "litv_"+uuid.NewString()
	path := filepath.ToSlash(filepath.Join("literature-versions", versionID[:7], versionID+".blob"))
	stageRelative, err := s.blobRelative(staging)
	if err != nil {
		return receipt, err
	}
	marker := blobCommitMarker{ID: uuid.NewString(), Kind: "literature-attachment-version", AggregateID: versionID, StagingPath: stageRelative, FinalPath: path, SHA256: digest, SizeBytes: size, CreatedAt: s.now().UTC()}
	version := LiteratureVersion{ID: versionID, AttachmentID: attachmentID, VersionNumber: 1, Filename: artifact.Name, MIMEType: "application/pdf", SizeBytes: size, Checksum: digest, PageCount: input.PageCount, Available: true, StoragePath: path}
	item := LiteratureItem{ID: itemID, Title: strings.TrimSpace(input.Title), MetadataRevision: 1, Metadata: frozenMetadata, ProjectIDs: []string{}, Attachments: []LiteratureAttachment{{ID: attachmentID, Kind: "fullText", Versions: []LiteratureVersion{version}}}}
	if input.ProjectID != "" {
		item.ProjectIDs = append(item.ProjectIDs, input.ProjectID)
	}
	receipt = LiteratureReceipt{Schema: "x-science.literature-receipt.v1", OperationID: input.OperationID, Item: item, AttachmentID: attachmentID, VersionID: versionID}
	err = s.WithTransaction(ctx, func(tx *sql.Tx) error {
		var prior LiteratureReceipt
		if found, err := lookupLiteratureMutationResultTx(ctx, tx, owner, input.OperationID, "literature.import", identity, &prior); err != nil {
			return err
		} else if found {
			receipt = prior
			return nil
		}
		var valid bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artifact_versions v JOIN artifacts a ON a.id=v.artifact_id JOIN projects p ON p.id=a.project_id WHERE v.id=? AND a.id=? AND p.user_id=? AND lower(v.content_sha256)=?)`, input.VersionID, input.ArtifactID, owner, digest).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrLiteratureUnavailable
		}
		if input.ProjectID != "" {
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=? AND user_id=?)`, input.ProjectID, owner).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return ErrLiteratureUnavailable
			}
		}
		now := s.now().UTC()
		if _, err := tx.ExecContext(ctx, `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,?,?,?,?,?)`, itemID, owner, item.Title, metadata, now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO literature_attachments(id,item_id,kind,created_at) VALUES(?,?,'fullText',?)`, attachmentID, itemID, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO literature_attachment_versions(id,attachment_id,version_number,filename,mime_type,size_bytes,checksum,page_count,storage_path,origin_version_id,created_at) VALUES(?,?,1,?,'application/pdf',?,?,?,?,?,?)`, versionID, attachmentID, version.Filename, size, digest, input.PageCount, path, input.VersionID, now); err != nil {
			return err
		}
		if input.ProjectID != "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO project_literature(project_id,item_id,added_at) VALUES(?,?,?)`, input.ProjectID, itemID, now); err != nil {
				return err
			}
		}
		if err := s.insertBlobCommitMarkerTx(ctx, tx, marker); err != nil {
			return err
		}
		return insertMutationResultTx(ctx, tx, owner, input.OperationID, "literature.import", identity.requestHash(), receipt, now)
	})
	if err != nil {
		return receipt, err
	}
	if receipt.VersionID != versionID {
		return receipt, s.recoverLiteratureImportContent(ctx, owner, receipt.VersionID)
	}
	keep = true
	return receipt, s.finalizeBlobCommit(ctx, marker)
}

func (s *Store) recoverLiteratureImportContent(ctx context.Context, owner, id string) error {
	if err := s.finalizeLiteratureVersion(ctx, id); err != nil {
		return err
	}
	_, _, reader, err := s.OpenLiteratureVersionContent(ctx, owner, id, true)
	if err != nil {
		return err
	}
	return reader.Close()
}

func (s *Store) finalizeLiteratureVersion(ctx context.Context, id string) error {
	var marker blobCommitMarker
	err := s.db.QueryRowContext(ctx, `SELECT id,kind,aggregate_id,staging_path,final_path,sha256,size_bytes,created_at FROM blob_commit_markers WHERE kind='literature-attachment-version' AND aggregate_id=?`, id).Scan(&marker.ID, &marker.Kind, &marker.AggregateID, &marker.StagingPath, &marker.FinalPath, &marker.SHA256, &marker.SizeBytes, &marker.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.finalizeBlobCommit(ctx, marker)
}
