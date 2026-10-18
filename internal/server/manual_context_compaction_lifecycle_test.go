package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestManualContextCompactionReplayCancellationAndScope(t *testing.T) {
	for _, scenario := range []string{"replay", "preaccept-cancel", "scope", "owner"} {
		t.Run(scenario, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			finishSkillControlInput(t, f)
			request := typedManualCompactionRequestForTest(t, f, "c9476bfa-0901-4ba1-abf7-07f2ce9314d5")
			path := "/api/conversations/" + f.stream.FrameID + "/context-compactions"
			if scenario == "owner" {
				result := p3JSONRequest(t, f.server, http.MethodPost, path, request, "another-owner")
				if result.Code != 404 {
					t.Fatalf("foreign HTTP %d: %s", result.Code, result.Body.String())
				}
				return
			}
			if scenario == "scope" {
				request.Expected.RootFrameIncarnationID = "retired-root"
				result := p3JSONRequest(t, f.server, http.MethodPost, path, request, f.stream.OwnerID)
				if result.Code != 409 {
					t.Fatalf("stale HTTP %d: %s", result.Code, result.Body.String())
				}
				return
			}
			if scenario == "preaccept-cancel" {
				absent := p3JSONRequest(t, f.server, http.MethodGet, manualCompactionReadPathForTest(path, request), nil, f.stream.OwnerID)
				if absent.Code != 404 {
					t.Fatalf("not accepted GET=%d", absent.Code)
				}
				cancel := p3JSONRequest(t, f.server, http.MethodDelete, path+"/by-request/"+request.RequestID, request, f.stream.OwnerID)
				if cancel.Code != 200 {
					t.Fatalf("cancel HTTP %d: %s", cancel.Code, cancel.Body.String())
				}
				receipt := p3DecodeObject(t, cancel)
				if receipt["accepted"] != false || receipt["cancellation_status"] != "cancelled_before_acceptance" || receipt["input_message_id"] != nil {
					t.Fatalf("unaccepted cancellation invented input: %#v", receipt)
				}
				late := p3JSONRequest(t, f.server, http.MethodPost, path, request, f.stream.OwnerID)
				if late.Code != 409 || p3DecodeObject(t, late)["code"] != "CONTEXT_COMPACTION_CANCELLED" {
					t.Fatalf("late POST HTTP %d: %s", late.Code, late.Body.String())
				}
				return
			}
			first := p3JSONRequest(t, f.server, http.MethodPost, path, request, f.stream.OwnerID)
			if first.Code != 202 {
				t.Fatalf("first HTTP %d: %s", first.Code, first.Body.String())
			}
			accepted := p3DecodeObject(t, first)
			replay := p3JSONRequest(t, f.server, http.MethodPost, path, request, f.stream.OwnerID)
			if replay.Code != 202 {
				t.Fatalf("busy replay HTTP %d: %s", replay.Code, replay.Body.String())
			}
			receipt := p3DecodeObject(t, replay)
			if receipt["input_message_id"] != accepted["input_message_id"] || receipt["replayed"] != true {
				t.Fatalf("replay=%#v", receipt)
			}
			changed := request
			changed.Expected.ThroughPublicationSeq++
			conflict := p3JSONRequest(t, f.server, http.MethodPost, path, changed, f.stream.OwnerID)
			if conflict.Code != 409 {
				t.Fatalf("changed body HTTP %d: %s", conflict.Code, conflict.Body.String())
			}
		})
	}
}

func manualCompactionReadPathForTest(path string, request transcriptstore.ContextCompactionRequest) string {
	target := request.Expected
	query := url.Values{"expected_frame_incarnation_id": {target.FrameIncarnationID}, "expected_root_frame_id": {target.RootFrameID},
		"expected_root_frame_incarnation_id": {target.RootFrameIncarnationID}, "expected_stream_uid": {target.StreamUID},
		"expected_stream_epoch": {strconv.FormatInt(target.StreamEpoch, 10)}}
	return path + "/by-request/" + request.RequestID + "?" + query.Encode()
}

func TestManualContextCompactionCancelFencesLateClaimAndSettlement(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	finishSkillControlInput(t, f)
	request := typedManualCompactionRequestForTest(t, f, "c9476bfa-0901-4ba1-abf7-07f2ce9314d5")
	accepted, err := f.store.AdmitContextCompaction(context.Background(), f.stream.OwnerID, f.stream.FrameID, request)
	if err != nil {
		t.Fatal(err)
	}
	claimSkillControlInput(t, f)
	oldClaim := f.claim
	cancelled, err := f.server.cancelContextCompactionAndDrain(context.Background(), f.stream.OwnerID, f.stream.FrameID, request)
	if err != nil || cancelled.State != "cancelled" || !cancelled.CanSubmitNextInput || cancelled.ExecutionObservation != "unavailable" {
		t.Fatalf("missing local owner must fence without physical claim: %#v err=%v", cancelled, err)
	}
	appendSkillControlInput(t, f, "after-cancel", "The next ordinary prompt.", false)
	claimSkillControlInput(t, f)
	current := f.claim
	if _, err = f.repo.FinishContextCompaction(context.Background(), oldClaim, request, manualCompactionBoundaryForTest(request)); !errors.Is(err, transcriptstore.ErrClaimStale) {
		t.Fatalf("late boundary err=%v", err)
	}
	_, err = f.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID,
		RunnerID: "late-maintenance", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
		ExpectedInputMessageID: accepted.InputMessageID, ExpectedInputRevision: accepted.InputRevision})
	if !errors.Is(err, transcriptstore.ErrClaimStale) {
		t.Fatalf("late maintenance claim err=%v", err)
	}
	state, err := f.repo.GetRunnerRuntimeState(context.Background(), f.stream.UID, f.stream.OwnerID, current.Attempt)
	if err != nil || state.Status != "running" || state.ClaimedInputRevision != current.ClaimedInputRevision {
		t.Fatalf("new input touched=%#v err=%v", state, err)
	}
}
