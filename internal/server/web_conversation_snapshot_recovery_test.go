package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

type canonicalHostInputFixture struct {
	*agentSaveArtifactsFixture
	frame workspace.CompatibilityFrame
	root  string
	body  map[string]any
}

func newCanonicalHostInputFixture(t *testing.T) canonicalHostInputFixture {
	t.Helper()
	f := newAsideAcceptanceFixture(t)
	frame, found, err := f.store.GetCompatibilityFrame(f.stream.FrameID)
	if err != nil || !found {
		t.Fatalf("frame found=%v err=%v", found, err)
	}
	root := t.TempDir()
	if _, err := f.server.upsertHostGrant(f.stream.OwnerID, root, "read"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "snapshot.txt"), []byte("original snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	uri := hostFileReferenceURI(root, "snapshot.txt")
	return canonicalHostInputFixture{f, frame, root, map[string]any{
		"content": "Inspect the original input.\n[[X_SCIENCE_FILES]]\n" + uri,
		"files":   []string{uri}, "loading_id": "exact-snapshot-input",
	}}
}

func (f canonicalHostInputFixture) counts(t *testing.T) [2]int {
	t.Helper()
	var counts [2]int
	if err := f.db.QueryRow(`SELECT count(*) FROM transcript_runner_attempts WHERE stream_uid=?`, f.stream.UID).Scan(&counts[0]); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT count(*) FROM frame_events WHERE frame_id=? AND event_type='frame_resumed'`, f.frame.ID).Scan(&counts[1]); err != nil {
		t.Fatal(err)
	}
	return counts
}

func TestWebConversationHostSnapshotReplayDoesNotRestartTerminalInput(t *testing.T) {
	for _, status := range []string{"completed", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			f := newCanonicalHostInputFixture(t)
			path := "/api/conversations/" + f.frame.ID + "/messages"
			first := p3JSONRequest(t, f.server, http.MethodPost, path, f.body, f.stream.OwnerID)
			if first.Code != http.StatusAccepted {
				t.Fatalf("send=%d %s", first.Code, first.Body.String())
			}
			originalID := p3DecodeObject(t, first)["msg_id"]
			claimed, err := f.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
				StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: "terminal-replay-owner", TTL: time.Minute,
				ResumeSource: transcriptstore.ResumeSourceFresh,
			})
			if err != nil || !claimed.Claimed {
				t.Fatalf("claim=%#v err=%v", claimed, err)
			}
			payload, _ := json.Marshal(map[string]any{"status": status})
			if _, _, _, err := f.repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{
				Claim: claimed.Claim, ClientMessageID: "original-terminal", Status: status, PayloadJSON: payload,
			}); err != nil {
				t.Fatal(err)
			}
			before := f.counts(t)
			if _, err := f.server.revokeHostGrant(f.stream.OwnerID, f.root); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(f.root, "snapshot.txt")); err != nil {
				t.Fatal(err)
			}
			retry := p3JSONRequest(t, f.server, http.MethodPost, path, f.body, f.stream.OwnerID)
			if retry.Code != http.StatusAccepted {
				t.Fatalf("retry=%d %s", retry.Code, retry.Body.String())
			}
			response := p3DecodeObject(t, retry)
			runtime, _ := response["runtime"].(map[string]any)
			receipt, _ := response["input_receipt"].(map[string]any)
			wantStatus := map[string]string{"completed": "finished", "failed": "error", "cancelled": "cancelled"}[status]
			if response["msg_id"] != originalID || receipt["input_message_id"] != originalID || receipt["state"] != "accepted" || receipt["replayed"] != true ||
				runtime["state"] != "idle" || runtime["task_status"] != wantStatus || runtime["is_processing"] != false || runtime["turn_id"] != nil {
				t.Fatalf("replay invented another active turn: %#v", response)
			}
			if after := f.counts(t); before != after {
				t.Fatalf("historical retry dispatched again: before=%v after=%v", before, after)
			}
		})
	}
}

func TestWebConversationHostSnapshotReplayRepairsOnlyAppendDispatchGap(t *testing.T) {
	f := newCanonicalHostInputFixture(t)
	failed := "failed"
	if _, err := f.store.UpdateFrame(f.frame.ID, workspace.UpdateFrameInput{Status: &failed}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`CREATE TRIGGER reject_snapshot_resume BEFORE INSERT ON frame_events
		WHEN NEW.event_type='frame_resumed' BEGIN SELECT RAISE(ABORT,'fixture append-before-dispatch gap'); END`); err != nil {
		t.Fatal(err)
	}
	path := "/api/conversations/" + f.frame.ID + "/messages"
	first := p3JSONRequest(t, f.server, http.MethodPost, path, f.body, f.stream.OwnerID)
	// The established SQL-constraint adapter returns 400 for this deliberate
	// RAISE(ABORT). Require the exact injected boundary, not just any failure.
	if first.Code != http.StatusBadRequest || !strings.Contains(first.Body.String(), "fixture append-before-dispatch gap") {
		t.Fatalf("injected dispatch failure=%d %s", first.Code, first.Body.String())
	}
	if before := f.counts(t); before[1] != 0 {
		t.Fatalf("failed dispatch was not rolled back: %v", before)
	}
	if _, err := f.db.Exec(`DROP TRIGGER reject_snapshot_resume`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.revokeHostGrant(f.stream.OwnerID, f.root); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		retry := p3JSONRequest(t, f.server, http.MethodPost, path, f.body, f.stream.OwnerID)
		if retry.Code != http.StatusAccepted {
			t.Fatalf("gap recovery=%d %s", retry.Code, retry.Body.String())
		}
		if counts := f.counts(t); counts[0] != 1 || counts[1] != 1 {
			t.Fatalf("gap recovery duplicated claim/dispatch: %v", counts)
		}
	}
	if got, err := os.ReadFile(filepath.Join(f.projectPath, "inputs/snapshot.txt")); err != nil || string(got) != "original snapshot" {
		t.Fatalf("recovery replaced its owned input: %q %v", got, err)
	}
}

func TestWebConversationHostSnapshotReplayCancellationCannotResurrectInput(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "queued"}[queued], func(t *testing.T) {
			f := newCanonicalHostInputFixture(t)
			path := "/api/conversations/" + f.frame.ID
			if queued {
				prior := p3JSONRequest(t, f.server, http.MethodPost, path+"/messages", map[string]any{"content": "Earlier input", "loading_id": "earlier-input"}, f.stream.OwnerID)
				if prior.Code != http.StatusAccepted {
					t.Fatalf("earlier=%d %s", prior.Code, prior.Body.String())
				}
			}
			first := p3JSONRequest(t, f.server, http.MethodPost, path+"/messages", f.body, f.stream.OwnerID)
			if first.Code != http.StatusAccepted {
				t.Fatalf("send=%d %s", first.Code, first.Body.String())
			}
			cancel := p3JSONRequest(t, f.server, http.MethodDelete, path+"/message-intents/exact-snapshot-input", f.body, f.stream.OwnerID)
			if cancel.Code != http.StatusOK {
				t.Fatalf("cancel=%d %s", cancel.Code, cancel.Body.String())
			}
			before := f.counts(t)
			if _, err := f.server.revokeHostGrant(f.stream.OwnerID, f.root); err != nil {
				t.Fatal(err)
			}
			retry := p3JSONRequest(t, f.server, http.MethodPost, path+"/messages", f.body, f.stream.OwnerID)
			if retry.Code != http.StatusConflict || p3DecodeObject(t, retry)["code"] != "MESSAGE_INTENT_CANCELLED" {
				t.Fatalf("cancelled snapshot resurrected: %d %s", retry.Code, retry.Body.String())
			}
			if after := f.counts(t); before != after {
				t.Fatalf("cancelled retry created work: %v %v", before, after)
			}
		})
	}
}
