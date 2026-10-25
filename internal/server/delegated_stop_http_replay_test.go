package server

import (
	"encoding/json"
	"net/http"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestDelegatedStopHTTPReplayFlagSurvivesCanonicalReadback(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	children, err := f.store.CreateKernelSupervisedChildren(t.Context(), workspace.CreateKernelDelegatesInput{
		ParentFrameID: f.stream.FrameID, OwnerUserID: f.stream.OwnerID, ToolUseID: "http-stop-replay-transport",
		Requests: []workspace.KernelDelegateRequest{{Task: "Actual idempotent Stop request"}},
	})
	if err != nil || len(children) != 1 {
		t.Fatalf("create real child=%#v err=%v", children, err)
	}
	child := children[0]
	ownerCtx, _, finishOwner := delegatedActiveRun(t, f, child, "http-replay-owner")
	ownerExited := make(chan struct{})
	go func() { <-ownerCtx.Done(); finishOwner(); close(ownerExited) }()
	t.Cleanup(func() {
		f.server.stopActiveSessionRun(child.FrameID, "HTTP replay fixture cleanup")
		awaitDelegatedOwnedExit(t, ownerExited)
	})
	request, body := delegatedAPIRequest(t, f, child.FrameID)
	path := "/api/frames/" + f.stream.FrameID + "/delegated-stops"
	read := func(method, url string, input any) workspace.DelegatedStopReceipt {
		t.Helper()
		response := p3JSONRequest(t, f.server, method, url, input, f.stream.OwnerID)
		if response.Code != http.StatusOK {
			t.Fatalf("HTTP%s=%d %s", method, response.Code, response.Body.String())
		}
		var receipt workspace.DelegatedStopReceipt
		var fields map[string]json.RawMessage
		if json.Unmarshal(response.Body.Bytes(), &receipt) != nil || json.Unmarshal(response.Body.Bytes(), &fields) != nil || fields["replayed"] == nil {
			t.Fatalf("missing actual replay field in receipt: %s", response.Body.String())
		}
		if receipt.Request.RequestID != request.RequestID || receipt.Request.Scope != request.Scope || len(receipt.Targets) != 1 || receipt.Targets[0].Target != *request.Target {
			t.Fatalf("transport readback changed the original Stop identity: %#v", receipt)
		}
		return receipt
	}
	if first := read(http.MethodPost, path, body); first.Replayed {
		t.Fatalf("first actual admission was labelled replay: %#v", first)
	}
	awaitDelegatedOwnedExit(t, ownerExited)
	stored, found, err := f.server.readDelegatedStopReceipt(t.Context(), f.stream.OwnerID, f.stream.FrameID, request.RequestID)
	if err != nil || !found || stored.State != "settled" || stored.Replayed {
		t.Fatalf("canonical completion before replay=%#v found=%t err=%v", stored, found, err)
	}
	snapshot := func() (string, int, int) {
		t.Helper()
		var payload string
		var receiptCount, wakes int
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE frame_id=? AND event_type=?`, f.stream.FrameID, workspace.DelegatedStopReceiptEventType).Scan(&receiptCount); err != nil {
			t.Fatal(err)
		}
		if err := f.db.QueryRow(`SELECT payload FROM frame_events WHERE frame_id=? AND event_type=?`, f.stream.FrameID, workspace.DelegatedStopReceiptEventType).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM workspace_outbox WHERE event_type='conversation.listChanged' AND json_extract(payload_json,'$.event.frameId')=? AND json_extract(payload_json,'$.event.payload.delegated_stop_changed')=1`, f.stream.FrameID).Scan(&wakes); err != nil {
			t.Fatal(err)
		}
		return payload, receiptCount, wakes
	}
	payloadBefore, countBefore, wakesBefore := snapshot()
	if countBefore != 1 || wakesBefore != 2 {
		t.Fatalf("initial actual receipt/observation count=%d/%d", countBefore, wakesBefore)
	}
	for range 2 {
		replayed := read(http.MethodPost, path, body)
		if !replayed.Replayed || replayed.State != "settled" {
			t.Fatalf("same actual POST lost replay metadata or durable outcome: %#v", replayed)
		}
	}
	if canonical := read(http.MethodGet, path+"/"+request.RequestID, nil); canonical.Replayed || canonical.State != "settled" {
		t.Fatalf("POST-only replay flag leaked into canonical GET: %#v", canonical)
	}
	if payloadAfter, countAfter, wakesAfter := snapshot(); payloadAfter != payloadBefore || countAfter != countBefore || wakesAfter != wakesBefore {
		t.Fatalf("transport replay changed durable receipt or duplicated observation: receipt count=%d wakes=%d payload_changed=%t", countAfter, wakesAfter, payloadAfter != payloadBefore)
	}
}
