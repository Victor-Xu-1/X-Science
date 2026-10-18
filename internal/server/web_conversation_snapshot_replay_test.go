package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Replaying an accepted input must read the owned snapshot, not today's host
// source or permissions. These are real HTTP/admission/filesystem paths.
func TestWebConversationHostSnapshotReplayUsesOriginalAcceptance(t *testing.T) {
	for _, queued := range []bool{false, true} {
		mode := "accepted"
		if queued {
			mode = "queued"
		}
		for _, change := range []string{"source_changed", "grant_revoked", "source_removed"} {
			t.Run(mode+"/"+change, func(t *testing.T) {
				f := newAsideAcceptanceFixture(t)
				app, owner := f.server, f.stream.OwnerID
				frame, found, err := app.workspaceStore.GetCompatibilityFrame(f.stream.FrameID)
				if err != nil || !found {
					t.Fatalf("canonical frame found=%v err=%v", found, err)
				}
				root := t.TempDir()
				if _, err := app.upsertHostGrant(owner, root, "read"); err != nil {
					t.Fatal(err)
				}
				if queued {
					prior := p3JSONRequest(t, app, http.MethodPost, "/api/conversations/"+frame.ID+"/messages", map[string]any{
						"content": "An earlier accepted input owns this turn.", "loading_id": "earlier-input",
					}, owner)
					if prior.Code != http.StatusAccepted {
						t.Fatalf("earlier admission=%d %s", prior.Code, prior.Body.String())
					}
				}
				source := filepath.Join(root, "snapshot.csv")
				original := []byte("sample,value\nfirst,1\n")
				if err := os.WriteFile(source, original, 0o600); err != nil {
					t.Fatal(err)
				}
				uri := hostFileReferenceURI(root, "snapshot.csv")
				body := map[string]any{"content": "Inspect this input.\n[[X_SCIENCE_FILES]]\n" + uri,
					"files": []string{uri}, "loading_id": "frozen-snapshot-retry"}
				first := p3JSONRequest(t, app, http.MethodPost, "/api/conversations/"+frame.ID+"/messages", body, owner)
				if first.Code != http.StatusAccepted {
					t.Fatalf("first admission=%d %s", first.Code, first.Body.String())
				}
				var accepted struct {
					MessageID string `json:"msg_id"`
				}
				if err := json.Unmarshal(first.Body.Bytes(), &accepted); err != nil || accepted.MessageID == "" {
					t.Fatalf("first receipt=%s err=%v", first.Body.String(), err)
				}
				before, found, err := app.transcriptStore.GetFrameStreamBySession(context.Background(), owner, frame.ID)
				if err != nil || !found {
					t.Fatalf("before stream found=%v err=%v", found, err)
				}
				inputRoot := filepath.Join(f.projectPath, "inputs")
				beforeFiles, err := os.ReadDir(inputRoot)
				if err != nil {
					t.Fatal(err)
				}
				queuedBefore, queueFound, err := app.workspaceStore.GetCompatibilityMessageIntent(accepted.MessageID)
				if err != nil || queued != queueFound || queued && queuedBefore.State != "queued" {
					t.Fatalf("queue found=%v state=%q err=%v", queueFound, queuedBefore.State, err)
				}
				switch change {
				case "source_changed":
					err = os.WriteFile(source, []byte("different source bytes now"), 0o600)
				case "grant_revoked":
					_, err = app.revokeHostGrant(owner, root)
				case "source_removed":
					err = os.Remove(source)
				}
				if err != nil {
					t.Fatal(err)
				}
				retry := p3JSONRequest(t, app, http.MethodPost, "/api/conversations/"+frame.ID+"/messages", body, owner)
				if retry.Code != http.StatusAccepted {
					t.Fatalf("accepted retry reread mutable source/authorization: status=%d body=%s", retry.Code, retry.Body.String())
				}
				var replay struct {
					MessageID string `json:"msg_id"`
				}
				if err := json.Unmarshal(retry.Body.Bytes(), &replay); err != nil || replay.MessageID != accepted.MessageID {
					t.Fatalf("replay lost canonical identity: %s err=%v", retry.Body.String(), err)
				}
				after, found, err := app.transcriptStore.GetFrameStreamBySession(context.Background(), owner, frame.ID)
				if err != nil || !found || after.InputRevision != before.InputRevision {
					t.Fatalf("retry created another input admission: before=%#v after=%#v err=%v", before, after, err)
				}
				afterFiles, err := os.ReadDir(inputRoot)
				if err != nil || len(afterFiles) != len(beforeFiles) {
					t.Fatalf("retry created another snapshot: before=%d after=%d err=%v", len(beforeFiles), len(afterFiles), err)
				}
				if got, err := os.ReadFile(filepath.Join(inputRoot, "snapshot.csv")); err != nil || string(got) != string(original) {
					t.Fatalf("original task-owned snapshot changed: %q %v", got, err)
				}
				if queued {
					queuedAfter, found, err := app.workspaceStore.GetCompatibilityMessageIntent(accepted.MessageID)
					if err != nil || !found || !reflect.DeepEqual(queuedBefore, queuedAfter) {
						t.Fatalf("retry changed original queued snapshot: before=%#v after=%#v err=%v", queuedBefore, queuedAfter, err)
					}
				}
			})
		}
	}
}
