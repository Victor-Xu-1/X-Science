package transcript

import (
	"strings"
	"testing"
)

func TestPDFReadingSourceUnionNeverAcceptsMixedIdentities(t *testing.T) {
	artifact := PDFReadingSource{ArtifactID: "artifact", VersionID: "version"}
	library := PDFReadingSource{SourceKind: "literature-attachment-version", ItemID: "item", AttachmentID: "attachment", VersionID: "litv_version"}
	if !artifact.Valid() || !library.Valid() {
		t.Fatal("valid exact source union rejected")
	}
	for _, source := range []PDFReadingSource{
		{SourceKind: "literature-attachment-version", ArtifactID: "artifact", ItemID: "item", AttachmentID: "attachment", VersionID: "version"},
		{SourceKind: "artifact-version", ArtifactID: "artifact", ItemID: "item", VersionID: "version"},
		{SourceKind: "literature-attachment-version", ItemID: "item", VersionID: "version"},
		{SourceKind: "unknown", ArtifactID: "artifact", VersionID: "version"},
		{SourceKind: "literature-attachment-version", ItemID: "item", AttachmentID: "attachment", VersionID: " version"},
	} {
		if source.Valid() {
			t.Fatalf("mixed/invalid identity accepted: %#v", source)
		}
	}
	if artifact.Key() == library.Key() {
		t.Fatal("source namespaces collapsed")
	}
	binding := PDFReadingBinding{BindingID: "binding", SourceKind: "artifact-version", ArtifactID: "artifact", VersionID: "version", Name: "paper.pdf", MIMEType: "application/pdf", SizeBytes: 10, Checksum: strings.Repeat("0", 64), PageCount: 2, LinkedAt: "2026-10-22T00:00:00Z"}
	if !binding.Valid() {
		t.Fatal("existing artifact binding rejected")
	}
	binding.SourceKind = ""
	if binding.Valid() {
		t.Fatal("stored binding lost its required source discriminator")
	}
}
