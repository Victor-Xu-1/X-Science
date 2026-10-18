package server

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestPDFReadingContextLostAckReplayNeverResurrectsUnlinkedBinding(t *testing.T) {
	f, current, body := newPDFReadingFixture(t)
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir())
	replay := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", body, f.stream.OwnerID)
	if replay.Code != 200 || p3DecodeObject(t, replay)["changed"] != false {
		t.Fatalf("lost ACK re-opened PDF: HTTP %d %s", replay.Code, replay.Body.String())
	}
	removed := unlinkPDFReadingForTest(t, f, current)
	if removed.Revision != current.Revision+1 || len(removed.Bindings) != 0 {
		t.Fatalf("unlink=%#v", removed)
	}
	stale := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", body, f.stream.OwnerID)
	if stale.Code != 409 {
		t.Fatalf("old link revived binding: HTTP %d %s", stale.Code, stale.Body.String())
	}
	t.Setenv("PATH", originalPath)
	relink := body
	relink.OperationID = uuid.NewString()
	relink.PDFReadingExpected = readingExpectedForTest(removed)
	response := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", relink, f.stream.OwnerID)
	if response.Code != 200 {
		t.Fatalf("fresh link HTTP %d %s", response.Code, response.Body.String())
	}
	latest, err := f.store.GetPDFReadingContext(context.Background(), f.stream.OwnerID, f.stream.FrameID)
	if err != nil || len(latest.Bindings) != 1 || latest.Bindings[0].BindingID == current.Bindings[0].BindingID {
		t.Fatalf("generation=%#v err=%v", latest, err)
	}
	oldUnlink := pdfReadingMutationBody{Schema: "x-science.pdf-reading-unlink.v1", OperationID: uuid.NewString(),
		PDFReadingExpected: readingExpectedForTest(current), BindingID: current.Bindings[0].BindingID}
	rejected := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/unlink", oldUnlink, f.stream.OwnerID)
	if rejected.Code != 409 {
		t.Fatalf("old unlink touched replacement: %d", rejected.Code)
	}
	_, version, found, err := f.store.GetArtifactVersionMetadata(body.Sources[0].VersionID)
	if err != nil || !found || version.ContentSHA256 != current.Bindings[0].Checksum {
		t.Fatalf("unlink changed source: found=%t err=%v", found, err)
	}
}

func TestPDFReadingContextOwnerScopeCASAndReservedMetadata(t *testing.T) {
	f, current, body := newPDFReadingFixture(t)
	if response := p3JSONRequest(t, f.server, http.MethodGet, readingPathForTest(f), nil, "other-owner"); response.Code != 404 {
		t.Fatalf("foreign read=%d", response.Code)
	}
	stale := body
	stale.OperationID = uuid.NewString()
	stale.FrameIncarnationID = "retired-frame"
	if response := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", stale, f.stream.OwnerID); response.Code != 409 {
		t.Fatalf("retired scope=%d", response.Code)
	}
	missing := body
	missing.PDFReadingExpected.Revision = nil
	if response := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", missing, f.stream.OwnerID); response.Code != 400 {
		t.Fatalf("missing revision=%d", response.Code)
	}
	before, found, err := f.store.GetFrameRuntimeMetadata(f.stream.FrameID)
	if err != nil || !found {
		t.Fatal(err)
	}
	removed := unlinkPDFReadingForTest(t, f, current)
	before.ContextData["unrelated_runtime_state"] = "preserved"
	if _, err = f.store.SetFrameRuntimeMetadata(f.stream.FrameID, before); err != nil {
		t.Fatal(err)
	}
	latest, err := f.store.GetPDFReadingContext(context.Background(), f.stream.OwnerID, f.stream.FrameID)
	if err != nil || latest.Revision != removed.Revision || len(latest.Bindings) != 0 {
		t.Fatalf("stale generic RMW revived state: %#v err=%v", latest, err)
	}
	before.ContextData[transcriptstore.PDFReadingMetadataKey] = map[string]any{"forged": true}
	if _, err = f.store.SetFrameRuntimeMetadata(f.stream.FrameID, before); err != nil {
		t.Fatal(err)
	}
	latest, err = f.store.GetPDFReadingContext(context.Background(), f.stream.OwnerID, f.stream.FrameID)
	if err != nil || latest.Revision != removed.Revision {
		t.Fatalf("generic replacement became authority: %#v %v", latest, err)
	}
	empty, err := f.store.CreateFrame(workspace.CreateFrameInput{ID: "unbound-reading", ProjectID: f.stream.ProjectID, AgentName: "OPERON", Status: "completed", ConversationType: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.SetFrameRuntimeMetadata(empty.ID, workspace.FrameRuntimeMetadata{ContextData: map[string]any{
		transcriptstore.PDFReadingMetadataKey: before.ContextData[transcriptstore.PDFReadingMetadataKey]}}); err == nil {
		t.Fatal("generic metadata forged initial context")
	}
}
