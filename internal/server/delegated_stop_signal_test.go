package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func delegatedAPIRequest(t *testing.T, f *agentSaveArtifactsFixture, child string) (workspace.DelegatedStopRequest, map[string]any) {
	t.Helper()
	snapshot, err := f.store.DelegatedStopSnapshot(t.Context(), f.stream.OwnerID, f.stream.FrameID, "")
	if err != nil {
		t.Fatal(err)
	}
	request := workspace.DelegatedStopRequest{Schema: "x-science.delegated-stop.v1", RequestID: uuid.NewString(), Scope: snapshot.Scope, Branch: snapshot.Branch,
		ParentInputRevision: snapshot.ParentInputRevision, ExpectedFenceGeneration: snapshot.FenceGeneration, Mode: "child"}
	for _, target := range snapshot.Targets {
		if target.FrameID == child {
			request.Target = target.Target
		}
	}
	if request.Target == nil {
		t.Fatal("missing canonical target")
	}
	raw, _ := json.Marshal(request)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return request, body
}

func delegatedActiveRun(t *testing.T, f *agentSaveArtifactsFixture, child workspace.KernelSupervisedChild, runnerID string, owners ...*kernelChildRun) (context.Context, transcriptstore.RunnerClaim, func()) {
	t.Helper()
	stream, found, err := f.repo.GetFrameStreamBySession(t.Context(), child.OwnerUserID, child.FrameID)
	if err != nil || !found {
		t.Fatalf("stream=%#v %v", stream, err)
	}
	claimed, err := f.repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: runnerID, TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%#v %v", claimed, err)
	}
	parent := context.Background()
	if len(owners) == 1 {
		parent = context.WithValue(parent, kernelChildOwnerContextKey{}, owners[0])
	}
	ctx, run, finish, err := f.server.registerActiveSessionRun(parent, child.FrameID, runnerID)
	if err != nil {
		t.Fatal(err)
	}
	run.settlement.Lock()
	run.transcript = &transcriptRunnerAuthority{Stream: stream, Claim: claimed.Claim}
	run.settlement.Unlock()
	t.Cleanup(finish)
	return ctx, claimed.Claim, finish
}

func TestDelegatedStopHTTPCommitPrecedesSignalAndReplayPreservesReplacement(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	children, err := f.store.CreateKernelSupervisedChildren(t.Context(), workspace.CreateKernelDelegatesInput{ParentFrameID: f.stream.FrameID, OwnerUserID: f.stream.OwnerID, ToolUseID: "http-stop", Requests: []workspace.KernelDelegateRequest{{Task: "Stop target"}, {Task: "Keep sibling"}}})
	if err != nil {
		t.Fatal(err)
	}
	childCtx, childClaim, finishChild := delegatedActiveRun(t, f, children[0], "target-run")
	siblingCtx, siblingClaim, _ := delegatedActiveRun(t, f, children[1], "sibling-run")
	request, body := delegatedAPIRequest(t, f, children[0].FrameID)
	path := "/api/frames/" + f.stream.FrameID + "/delegated-stops"
	if _, err := f.db.Exec(`CREATE TRIGGER fail_stop_receipt BEFORE INSERT ON frame_events WHEN NEW.event_type='delegated_stop_receipt' BEGIN SELECT RAISE(ABORT,'controlled commit rejection'); END`); err != nil {
		t.Fatal(err)
	}
	failed := p3JSONRequest(t, f.server, http.MethodPost, path, body, f.stream.OwnerID)
	if failed.Code != http.StatusServiceUnavailable {
		t.Fatalf("failure HTTP%d %s", failed.Code, failed.Body.String())
	}
	if childCtx.Err() != nil || siblingCtx.Err() != nil {
		t.Fatal("rolled-back cancellation emitted a physical signal")
	}
	if _, err := f.repo.ValidateLiveRunnerClaim(t.Context(), childClaim); err != nil {
		t.Fatalf("rollback changed canonical child: %v", err)
	}
	if _, err := f.db.Exec(`DROP TRIGGER fail_stop_receipt`); err != nil {
		t.Fatal(err)
	}
	accepted := p3JSONRequest(t, f.server, http.MethodPost, path, body, f.stream.OwnerID)
	if accepted.Code != http.StatusOK {
		t.Fatalf("accepted HTTP%d %s", accepted.Code, accepted.Body.String())
	}
	if !errors.Is(context.Cause(childCtx), ErrGenerationStopped) {
		t.Fatalf("exact child signal=%v", context.Cause(childCtx))
	}
	if siblingCtx.Err() != nil {
		t.Fatal("sibling received child stop")
	}
	if _, err := f.repo.ValidateLiveRunnerClaim(t.Context(), siblingClaim); err != nil {
		t.Fatalf("sibling canonical state=%v", err)
	}
	finishChild()
	deadline := time.Now().Add(3 * time.Second)
	for {
		receipt, found, err := f.store.ReadDelegatedStop(t.Context(), f.stream.OwnerID, f.stream.FrameID, request.RequestID)
		if err != nil || !found {
			t.Fatalf("read receipt=%#v %v", receipt, err)
		}
		if receipt.State == "settled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("actual registration done not observed=%#v", receipt)
		}
		time.Sleep(time.Millisecond)
	}
	rows, err := f.db.Query(`SELECT payload_json FROM workspace_outbox WHERE event_type='conversation.listChanged' ORDER BY sequence`)
	if err != nil {
		t.Fatal(err)
	}
	wakes := 0
	for rows.Next() {
		var raw []byte
		var envelope workspace.RealtimeOutboxEnvelope
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Event.Payload["delegated_stop_changed"] == true {
			wakes++
			if envelope.Event.UserID != f.stream.OwnerID || envelope.Event.FrameID != f.stream.FrameID || envelope.Event.Payload["parent_frame_incarnation_id"] != request.Scope.ParentFrameIncarnationID {
				t.Fatalf("wrong scoped wake=%#v", envelope)
			}
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if wakes != 2 {
		t.Fatalf("want admission + physical settlement wake, got %d", wakes)
	}
	if _, _, _, err := f.repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{StreamUID: request.Target.StreamUID, OwnerID: f.stream.OwnerID, ClientMessageID: "next-child-input", FrameEventID: "next-child-event", MessageUUID: "next-child-uuid", Text: "User's next task", Destinations: []string{"ws"}}); err != nil {
		t.Fatal(err)
	}
	newCtx, newClaim, _ := delegatedActiveRun(t, f, children[0], "replacement-run")
	replay := p3JSONRequest(t, f.server, http.MethodPost, path, body, f.stream.OwnerID)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay HTTP%d %s", replay.Code, replay.Body.String())
	}
	if newCtx.Err() != nil || siblingCtx.Err() != nil {
		t.Fatal("old receipt cancelled replacement or sibling")
	}
	if _, err := f.repo.ValidateLiveRunnerClaim(t.Context(), newClaim); err != nil {
		t.Fatalf("replacement claim changed=%v", err)
	}
}

func TestDelegatedStopHTTPUnknownReadAndChangedBodyRemainNonMutating(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	children, err := f.store.CreateKernelSupervisedChildren(t.Context(), workspace.CreateKernelDelegatesInput{ParentFrameID: f.stream.FrameID, OwnerUserID: f.stream.OwnerID, ToolUseID: "http-validation", Requests: []workspace.KernelDelegateRequest{{Task: "Task"}}})
	if err != nil {
		t.Fatal(err)
	}
	request, body := delegatedAPIRequest(t, f, children[0].FrameID)
	path := "/api/frames/" + f.stream.FrameID + "/delegated-stops"
	unknown := p3JSONRequest(t, f.server, http.MethodGet, path+"/"+request.RequestID, nil, f.stream.OwnerID)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown=%d %s", unknown.Code, unknown.Body.String())
	}
	foreign := p3JSONRequest(t, f.server, http.MethodPost, path, body, "another-owner")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign=%d %s", foreign.Code, foreign.Body.String())
	}
	body["unexpected"] = true
	invalid := p3JSONRequest(t, f.server, http.MethodPost, path, body, f.stream.OwnerID)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("unknown field=%d %s", invalid.Code, invalid.Body.String())
	}
	delete(body, "unexpected")
	accepted := p3JSONRequest(t, f.server, http.MethodPost, path, body, f.stream.OwnerID)
	if accepted.Code != http.StatusOK {
		t.Fatalf("accepted=%d %s", accepted.Code, accepted.Body.String())
	}
	body["mode"] = "branch"
	delete(body, "target")
	conflict := p3JSONRequest(t, f.server, http.MethodPost, path, body, f.stream.OwnerID)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("changed body=%d %s", conflict.Code, conflict.Body.String())
	}
}
