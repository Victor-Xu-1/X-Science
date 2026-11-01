package workspace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func annotationPageSourceForTest(t *testing.T) (*Store, ArtifactAnnotationSource) {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "annotations.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "annotation-project", UserID: "annotation-owner", Name: "Synthetic annotations"}); err != nil {
		t.Fatal(err)
	}
	artifact, version, err := store.SaveArtifactVersion(SaveArtifactVersionInput{ArtifactID: "annotation-source", ProjectID: "annotation-project", Name: "synthetic.pdf", Kind: "pdf", Content: []byte("%PDF-synthetic-metadata-store-fixture")})
	if err != nil {
		t.Fatal(err)
	}
	return store, ArtifactAnnotationSource{OwnerID: "annotation-owner", ProjectID: artifact.ProjectID, ArtifactID: artifact.ID,
		VersionID: version.ID, Checksum: version.ContentSHA256, SizeBytes: version.SizeBytes}
}

func TestArtifactAnnotationReadPageBoundsKeysetAndChangedAnchor(t *testing.T) {
	store, source := annotationPageSourceForTest(t)
	ids := []string{}
	for index := 0; index < 21; index++ {
		row, err := store.CreateAnnotation(CreateAnnotationInput{ProjectID: source.ProjectID, TargetKind: "artifact", TargetKey: "av:" + source.VersionID,
			ContentChecksum: source.Checksum, Body: map[string]any{"type": "point", "text": fmt.Sprintf("note-%02d", index)}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, row.ID)
	}
	other, err := store.CreateAnnotation(CreateAnnotationInput{ProjectID: source.ProjectID, TargetKind: "artifact", TargetKey: "av:another-version", Body: map[string]any{"text": "outside this source"}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.ListArtifactAnnotationPageOwned(t.Context(), source, nil)
	if err != nil || first.Total != 21 || len(first.Items) != 20 || first.Next == nil || first.Items[19].ID != ids[19] {
		t.Fatalf("first page=%#v err=%v", first, err)
	}
	second, err := store.ListArtifactAnnotationPageOwned(t.Context(), source, first.Next)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != ids[20] || second.Next != nil || second.Total != 21 {
		t.Fatalf("second page=%#v err=%v", second, err)
	}
	foreign := &AnnotationPageCursor{ID: other.ID, LabelIndex: other.LabelIndex, CreatedAt: other.CreatedAt}
	if _, err := store.ListArtifactAnnotationPageOwned(t.Context(), source, foreign); !errors.Is(err, ErrAnnotationReadCursor) {
		t.Fatalf("cross-version anchor accepted: %v", err)
	}
	if _, err := store.DeleteAnnotation(first.Next.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListArtifactAnnotationPageOwned(t.Context(), source, first.Next); !errors.Is(err, ErrAnnotationReadCursor) {
		t.Fatalf("deleted anchor silently became a fresh page: %v", err)
	}
}

func TestArtifactAnnotationReadPageChecksOwnerResourceAndCancellation(t *testing.T) {
	store, source := annotationPageSourceForTest(t)
	page, err := store.ListArtifactAnnotationPageOwned(t.Context(), source, nil)
	if err != nil || page.Total != 0 || page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("valid bound empty source=%#v %v", page, err)
	}
	for _, change := range []func(*ArtifactAnnotationSource){
		func(s *ArtifactAnnotationSource) { s.OwnerID = "another-owner" },
		func(s *ArtifactAnnotationSource) { s.ProjectID = "another-project" },
		func(s *ArtifactAnnotationSource) { s.ArtifactID = "another-artifact" },
		func(s *ArtifactAnnotationSource) { s.VersionID = "another-version" },
		func(s *ArtifactAnnotationSource) { s.Checksum = "different" },
		func(s *ArtifactAnnotationSource) { s.SizeBytes++ },
	} {
		wrong := source
		change(&wrong)
		if _, err := store.ListArtifactAnnotationPageOwned(t.Context(), wrong, nil); !errors.Is(err, ErrAnnotationReadSource) {
			t.Fatalf("wrong source became a successful empty page: %#v err=%v", wrong, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.ListArtifactAnnotationPageOwned(ctx, source, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled SQL read=%v", err)
	}
	if _, err := store.db.Exec(`UPDATE artifact_versions SET content_available=0 WHERE id=?`, source.VersionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListArtifactAnnotationPageOwned(t.Context(), source, nil); !errors.Is(err, ErrAnnotationReadSource) {
		t.Fatalf("unavailable source became empty success: %v", err)
	}
}

func TestArtifactAnnotationReadPageRejectsMismatchedContentProvenance(t *testing.T) {
	store, source := annotationPageSourceForTest(t)
	if _, err := store.CreateAnnotation(CreateAnnotationInput{ProjectID: source.ProjectID, TargetKind: "artifact", TargetKey: "av:" + source.VersionID,
		ContentChecksum: "another-content-checksum", Body: map[string]any{"type": "point", "text": "wrong source"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListArtifactAnnotationPageOwned(t.Context(), source, nil); !errors.Is(err, ErrAnnotationReadSource) {
		t.Fatalf("annotation from different content was exposed: %v", err)
	}
}
