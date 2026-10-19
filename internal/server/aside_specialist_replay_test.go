package server

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

func TestAsideSpecialistAcceptedReplayPrecedesNewHeadAndRevocation(t *testing.T) {
	f, prepared := newAsideSpecialistFixture(t)
	const intent = "fa4d8d91-bd3e-4da0-9db8-fc924d880571"
	path, body := "/api/frames/"+f.stream.FrameID+"/aside", asideSpecialistBody(prepared, intent)
	created := p3JSONRequest(t, f.server, http.MethodPost, path, body, f.stream.OwnerID)
	if created.Code != http.StatusOK {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	first := p3DecodeObject(t, created)
	child := decodeAsideReference(t, first, "specialist_binding")
	later := prepareAsideSpecialist(t, f, "ASIDE_SYNTHETIC_B", "later-aside-source")
	if _, err := f.db.Exec(`UPDATE user_agents SET enabled=0,system_prompt='Changed after acceptance' WHERE user_id=? AND name=?`, f.stream.OwnerID, prepared.Snapshot.Desired.SelectionName); err != nil {
		t.Fatal(err)
	}
	retainAsideSpecialistReceipt(t, f, intent)
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		target := path
		var request any = body
		if method == http.MethodGet {
			target, request = path+"?intent_id="+intent, nil
		}
		response := p3JSONRequest(t, f.server, method, target, request, f.stream.OwnerID)
		if response.Code != http.StatusOK {
			t.Fatalf("replay blocked by today's head/catalog: %d %s", response.Code, response.Body.String())
		}
		proof := p3DecodeObject(t, response)
		if proof["frame_id"] != first["frame_id"] || proof["replayed"] != true || decodeAsideReference(t, proof, "source_specialist") != asideSpecialistReference(prepared) || decodeAsideReference(t, proof, "specialist_binding") != child {
			t.Fatalf("rebound replay: %#v", proof)
		}
	}
	changed := p3JSONRequest(t, f.server, http.MethodPost, path, asideSpecialistBody(later, intent), f.stream.OwnerID)
	if changed.Code != http.StatusConflict || p3DecodeObject(t, changed)["code"] != "ASIDE_INTENT_CONFLICT" {
		t.Fatalf("same nonce changed authority: %d %s", changed.Code, changed.Body.String())
	}
	var children, inputs int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM frames WHERE id<>?`, f.stream.FrameID).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='user_message'`, "frame:"+child.Scope.FrameID).Scan(&inputs); err != nil || children != 1 || inputs != 1 {
		t.Fatalf("duplicate children=%d inputs=%d err=%v", children, inputs, err)
	}
}

func TestAsideSpecialistCreateAndCancelShareOneAuthority(t *testing.T) {
	f, prepared := newAsideSpecialistFixture(t)
	const intent = "42ea91a9-7393-4853-9f1d-4f04a8f62641"
	path, body := "/api/frames/"+f.stream.FrameID+"/aside", asideSpecialistBody(prepared, intent)
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		target := path
		if method == http.MethodDelete {
			target += "?intent_id=" + intent
		}
		response := p3JSONRequest(t, f.server, method, target, body, f.stream.OwnerID)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", method, response.Code, response.Body.String())
		}
		proof := p3DecodeObject(t, response)
		if decodeAsideReference(t, proof, "source_specialist") != asideSpecialistReference(prepared) || decodeAsideReference(t, proof, "specialist_binding").SnapshotID != prepared.Receipt.SnapshotID {
			t.Fatalf("missing typed proof on %s: %#v", method, proof)
		}
		if method == http.MethodDelete && (proof["cancelled"] != true || proof["cancelled_input_message_id"] != intent) {
			t.Fatalf("wrong child input cancellation: %#v", proof)
		}
	}
	read := p3JSONRequest(t, f.server, http.MethodGet, fmt.Sprintf("%s?intent_id=%s", path, intent), nil, f.stream.OwnerID)
	if read.Code != http.StatusOK || p3DecodeObject(t, read)["cancelled_input_message_id"] != intent {
		t.Fatalf("cancel readback: %d %s", read.Code, read.Body.String())
	}
}

func TestAsideSpecialistCancellationRetainsOriginalSourceProof(t *testing.T) {
	f, prepared := newAsideSpecialistFixture(t)
	const intent = "2c577d01-d859-4197-b5d0-f371295c3859"
	path := "/api/frames/" + f.stream.FrameID + "/aside"
	body := asideSpecialistBody(prepared, intent)
	cancelled := p3JSONRequest(t, f.server, http.MethodDelete, path+"?intent_id="+intent, body, f.stream.OwnerID)
	if cancelled.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", cancelled.Code, cancelled.Body.String())
	}
	proof := p3DecodeObject(t, cancelled)
	if proof["accepted"] != false || proof["cancelled"] != true || decodeAsideReference(t, proof, "source_specialist") != asideSpecialistReference(prepared) || proof["specialist_binding"] != nil {
		t.Fatalf("cancel manufactured child or lost source: %#v", proof)
	}
	later := prepareAsideSpecialist(t, f, "ASIDE_SYNTHETIC_B", "prepare-after-cancel")
	retainAsideSpecialistReceipt(t, f, intent)
	read := p3JSONRequest(t, f.server, http.MethodGet, path+"?intent_id="+intent, nil, f.stream.OwnerID)
	if read.Code != http.StatusOK || decodeAsideReference(t, p3DecodeObject(t, read), "source_specialist") != asideSpecialistReference(prepared) {
		t.Fatalf("cancel readback=%s", read.Body.String())
	}
	for _, method := range []string{http.MethodDelete, http.MethodPost} {
		target := path
		if method == http.MethodDelete {
			target += "?intent_id=" + intent
		}
		original := p3JSONRequest(t, f.server, method, target, body, f.stream.OwnerID)
		want := http.StatusOK
		if method == http.MethodPost {
			want = http.StatusConflict
		}
		if original.Code != want || decodeAsideReference(t, p3DecodeObject(t, original), "source_specialist") != asideSpecialistReference(prepared) {
			t.Fatalf("original retry after head changed: %d %s", original.Code, original.Body.String())
		}
		changed := asideSpecialistBody(later, intent)
		conflict := p3JSONRequest(t, f.server, method, target, changed, f.stream.OwnerID)
		if conflict.Code != http.StatusConflict || p3DecodeObject(t, conflict)["code"] != "ASIDE_INTENT_CONFLICT" {
			t.Fatalf("changed source swallowed by old cancellation: %d %s", conflict.Code, conflict.Body.String())
		}
	}
	reopened, err := workspace.Open(f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if recovered, found, err := reopened.GetCompatibilityAsideAcceptance(t.Context(), f.stream.OwnerID, f.stream.FrameID, intent); err != nil || !found || recovered.Cancellation == nil {
		t.Fatalf("restart proof: %#v %t %v", recovered, found, err)
	}
	var children int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM frames WHERE id<>?`, f.stream.FrameID).Scan(&children); err != nil || children != 0 {
		t.Fatalf("cancel created children=%d err=%v", children, err)
	}
}

func retainAsideSpecialistReceipt(t *testing.T, f *agentSaveArtifactsFixture, intent string) {
	t.Helper()
	if _, err := f.db.Exec(`UPDATE queued_user_messages SET resolved_at=? WHERE intent_id=?`, time.Now().UTC().Add(-9*24*time.Hour), intent); err != nil {
		t.Fatal(err)
	}
	report, err := f.store.SweepDataLifecycle(t.Context(), workspace.DefaultDataLifecyclePolicy())
	if err != nil || report.ResolvedQueuedIntents != 0 {
		t.Fatalf("valid immutable aside receipt was not retained: %#v %v", report, err)
	}
}
