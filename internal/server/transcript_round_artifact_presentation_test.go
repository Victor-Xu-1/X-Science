package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	transcriptstore "synon-go/internal/persistence/transcript"
	workspace "synon-go/internal/persistence/workspace"
)

func saveRoundPresentationFile(t *testing.T, f *agentSaveArtifactsFixture, call, path, body string) map[string]any {
	t.Helper()
	write := writeAgentSaveArtifactsFile(t, f.projectPath, path, body)
	f.saveExecution(t, f.identity.access, f.projectPath, "execution-"+call, int(f.claim.Attempt), write)
	input := map[string]any{"files": []any{path}, "destination": map[string]any{path: "snapshot"}, "language": "text", "human_description": "Save generated result"}
	saved, err := f.server.executeAgentSaveArtifacts(f.toolContext(t, call, input), f.identity, call, input)
	if err != nil {
		t.Fatal(err)
	}
	return agentSaveArtifactResults(t, saved)[0]
}

func finishRoundPresentation(t *testing.T, f *agentSaveArtifactsFixture, status string) transcriptstore.TerminalProjection {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"status": status, "text": "Round ended"})
	event, _, _, err := f.repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{
		Claim: f.claim, ClientMessageID: fmt.Sprintf("finish-%d", f.claim.Attempt), Status: status, PayloadJSON: raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	projection, err := f.repo.GetTerminalProjection(context.Background(), f.stream.OwnerID, f.stream.UID, event.EventID)
	if err != nil {
		t.Fatal(err)
	}
	return projection
}

func roundPresentationMessage(f *agentSaveArtifactsFixture) map[string]any {
	id := fmt.Sprintf("assistant-%s-%d-segment-2", f.stream.FrameID, f.claim.Attempt)
	return map[string]any{"id": id, "type": "text", "position": "left", "terminal_status": "completed",
		"content": map[string]any{"content": "", "assistant_attempt_id": id}}
}

func startNextPresentationRound(t *testing.T, f *agentSaveArtifactsFixture) {
	t.Helper()
	key := fmt.Sprintf("input-after-%d", f.claim.Attempt)
	if _, _, _, err := f.repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: key,
		FrameEventID: key + "-event", MessageUUID: key + "-message", Text: "Next round",
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := f.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: f.claim.RunnerID,
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	f.claim = claimed.Claim
}

func TestRoundArtifactPresentationWaitsForCompletionAndMatchesLiveHistory(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	artifact := saveRoundPresentationFile(t, f, "save-origin", "result.txt", "real generated output")
	assistant, bound, _, err := f.repo.AppendAssistantEventWithCommittedArtifacts(context.Background(), transcriptstore.AppendEventInput{
		Claim: f.claim, ClientMessageID: "intermediate", Type: "assistant_message", Source: transcriptstore.EventSourcePayload,
		PayloadJSON: []byte(`{"text":"Saved; continuing analysis"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	refs := transcriptArtifactReferences(bound)
	tool := map[string]any{"type": "tool_call", "position": "left", "artifact_refs": refs}
	prose := map[string]any{"type": "text", "position": "left", "status": "finish", "artifact_refs": refs,
		"content": map[string]any{"content": "Saved; continuing analysis"}}
	user := map[string]any{"type": "text", "position": "right", "artifact_refs": []map[string]any{{"relation": "attached", "version_id": "upload"}}}
	premature := roundPresentationMessage(f)
	if err := f.server.enrichTranscriptWebArtifactPresentation(context.Background(), f.stream.FrameID, []map[string]any{tool, prose, user, premature}); err != nil {
		t.Fatal(err)
	}
	for _, message := range []map[string]any{tool, prose, premature} {
		if len(transcriptWebArtifactReferenceMaps(message["artifact_refs"])) > 0 {
			t.Fatalf("unfinished round gained attachments: %#v", message)
		}
	}
	if len(transcriptWebArtifactReferenceMaps(user["artifact_refs"])) == 0 {
		t.Fatal("user upload disappeared")
	}
	if err := f.server.publishTranscriptWebClaim(context.Background(), transcriptstore.DeliveryClaim{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, Event: assistant,
		ResolvedPayloadJSON: []byte(`{"text":"Saved; continuing analysis"}`), PublicationSeq: assistant.PublicationSeq, ArtifactReferences: bound,
	}); err != nil {
		t.Fatal(err)
	}
	for _, event := range transcriptWebEvents(t, f.store, f.stream.OwnerID) {
		if event.Type == "message.stream" && len(transcriptWebArtifactReferenceMaps(event.Payload["artifact_refs"])) > 0 {
			t.Fatal("live intermediate publication exposed attachments")
		}
	}
	projection := finishRoundPresentation(t, f, "completed")
	terminal := roundPresentationMessage(f)
	// A page containing only the terminal answer still receives the complete delivery.
	if err := f.server.enrichTranscriptWebArtifactPresentation(context.Background(), f.stream.FrameID, []map[string]any{terminal}); err != nil {
		t.Fatal(err)
	}
	historyRefs := transcriptWebArtifactReferenceMaps(terminal["artifact_refs"])
	if len(historyRefs) != 1 || webString(historyRefs[0]["version_id"]) != stringValue(artifact["version_id"]) {
		t.Fatalf("terminal delivery=%#v", historyRefs)
	}
	frameContext, found, err := f.store.GetFrameRealtimeContext(f.stream.FrameID)
	if err != nil || !found {
		t.Fatalf("frame context found=%t err=%v", found, err)
	}
	if err := f.server.publishTranscriptTerminal(context.Background(), frameContext, "round-terminal", projection, webString(terminal["id"])); err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, event := range transcriptWebEvents(t, f.store, f.stream.OwnerID) {
		if event.Type == "message.stream" && event.Payload["terminal_status"] == "completed" {
			matched = true
			liveJSON, _ := json.Marshal(transcriptWebArtifactReferenceMaps(event.Payload["artifact_refs"]))
			historyJSON, _ := json.Marshal(historyRefs)
			if string(liveJSON) != string(historyJSON) {
				t.Fatalf("live/history delivery differs: %#v versus %#v", event.Payload, historyRefs)
			}
		}
	}
	if !matched {
		t.Fatal("no actual terminal publication")
	}
	if err := f.server.enrichTranscriptWebArtifactPresentation(context.Background(), f.stream.FrameID, []map[string]any{terminal}); err != nil || !reflect.DeepEqual(terminal["artifact_refs"], historyRefs) {
		t.Fatalf("reload changed delivery: %#v %v", terminal, err)
	}
}

func TestRoundArtifactPresentationKeepsHeadsWithinInputAndBranch(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	first := saveRoundPresentationFile(t, f, "first", "result.txt", "first version")
	saveRoundPresentationFile(t, f, "old-only", "old.txt", "previous round only")
	firstAttempt := f.claim.Attempt
	finishRoundPresentation(t, f, "completed")
	startNextPresentationRound(t, f)
	saveRoundPresentationFile(t, f, "second-draft", "result.txt", "intermediate revision")
	latest := saveRoundPresentationFile(t, f, "second-final", "result.txt", "final second version")
	secondAttempt := f.claim.Attempt
	finishRoundPresentation(t, f, "completed")
	startNextPresentationRound(t, f)
	finishRoundPresentation(t, f, "completed")
	rounds, err := f.repo.CompletedRoundArtifactReferences(context.Background(), f.stream.UID, f.stream.OwnerID, []int64{firstAttempt, secondAttempt, f.claim.Attempt, secondAttempt})
	if err != nil || len(rounds[firstAttempt]) != 2 || len(rounds[secondAttempt]) != 1 || len(rounds[f.claim.Attempt]) != 0 {
		t.Fatalf("rounds=%#v err=%v", rounds, err)
	}
	if rounds[firstAttempt][0].VersionID != stringValue(first["version_id"]) || rounds[secondAttempt][0].VersionID != stringValue(latest["version_id"]) {
		t.Fatal("a newer round changed an older delivered version or a draft survived")
	}
	if _, err := f.repo.CompletedRoundArtifactReferences(context.Background(), f.stream.UID, "foreign-owner", []int64{firstAttempt}); !errors.Is(err, transcriptstore.ErrOwnerMismatch) {
		t.Fatalf("owner isolation error=%v", err)
	}
	if _, err := f.repo.CompletedRoundArtifactReferences(context.Background(), f.stream.UID, f.stream.OwnerID, []int64{0}); !errors.Is(err, transcriptstore.ErrEventConflict) {
		t.Fatalf("invalid attempt error=%v", err)
	}
}

func TestRoundArtifactPresentationDoesNotPublishCancelledOrFailedOutput(t *testing.T) {
	for _, status := range []string{"failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			saveRoundPresentationFile(t, f, "partial", "partial.txt", "preserved partial output")
			finishRoundPresentation(t, f, status)
			rounds, err := f.repo.CompletedRoundArtifactReferences(context.Background(), f.stream.UID, f.stream.OwnerID, []int64{f.claim.Attempt})
			if err != nil || len(rounds[f.claim.Attempt]) != 0 {
				t.Fatalf("non-completed round published: %#v %v", rounds, err)
			}
		})
	}
}

func TestRoundArtifactPresentationCarriesSavedOutputAcrossAuthorizedResume(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	before := saveRoundPresentationFile(t, f, "before-resume", "before.txt", "saved before interruption")
	first := f.claim
	finishRoundPresentation(t, f, "failed")
	status := "failed"
	if _, err := f.store.UpdateFrame(f.stream.FrameID, workspace.UpdateFrameInput{Status: &status}); err != nil {
		t.Fatal(err)
	}
	resume, err := f.store.ResumeCompatibilityFrameConversation(f.stream.RootFrameID, workspace.ResumeCompatibilityFrameInput{})
	if err != nil || resume.Event == nil {
		t.Fatalf("resume=%#v err=%v", resume, err)
	}
	dispatch, claimed, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("round-resume-dispatch", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("dispatch claimed=%t err=%v", claimed, err)
	}
	next, err := f.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: "frame-resume:" + dispatch.ResumeEvent.ID,
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceUserInput,
	})
	if err != nil || !next.Claimed || next.Claim.Attempt <= first.Attempt || next.Claim.ClaimedInputRevision != first.ClaimedInputRevision {
		t.Fatalf("resumed=%#v err=%v", next, err)
	}
	f.claim = next.Claim
	after := saveRoundPresentationFile(t, f, "after-resume", "after.txt", "saved after recovery")
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	commits, err := f.server.sessionRunnerArtifactCommitReferences(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := f.server.sessionRunnerActiveArtifactCommitReferences(context.Background(), run, commits, "Finished")
	if err != nil || len(selected) != 2 {
		t.Fatalf("same-input recovered publication selected=%#v err=%v", selected, err)
	}
	pageInput := workspace.CompatibilityProjectArtifactPageInput{
		OwnerUserID: f.stream.OwnerID, ProjectID: f.stream.ProjectID, FrameID: f.stream.FrameID,
		ExcludeIntermediate: true, ExcludeInternal: true, Limit: 1,
	}
	page, err := f.store.ListCompatibilityProjectCurrentArtifactPage(context.Background(), pageInput)
	if err != nil || page.Total != 0 {
		t.Fatalf("unfinished recovery exposed drafts: %#v %v", page, err)
	}
	finishRoundPresentation(t, f, "completed")
	rounds, err := f.repo.CompletedRoundArtifactReferences(context.Background(), f.stream.UID, f.stream.OwnerID, []int64{first.Attempt, f.claim.Attempt})
	if err != nil || len(rounds[first.Attempt]) != 0 || len(rounds[f.claim.Attempt]) != 2 {
		t.Fatalf("resumed round=%#v err=%v", rounds, err)
	}
	if rounds[f.claim.Attempt][0].VersionID != stringValue(before["version_id"]) || rounds[f.claim.Attempt][1].VersionID != stringValue(after["version_id"]) {
		t.Fatal("recovery lost or changed original saved versions")
	}
	page, err = f.store.ListCompatibilityProjectCurrentArtifactPage(context.Background(), pageInput)
	if err != nil || page.Total != 2 || len(page.Artifacts) != 1 || !page.HasMore {
		t.Fatalf("historical collection differs from round: %#v %v", page, err)
	}
	if page.Artifacts[0].IsIntermediate {
		t.Fatal("completed delivery retained a draft presentation")
	}
	pageInput.Cursor = page.NextCursor
	nextPage, err := f.store.ListCompatibilityProjectCurrentArtifactPage(context.Background(), pageInput)
	if err != nil || len(nextPage.Artifacts) != 1 || nextPage.HasMore || nextPage.Artifacts[0].ID == page.Artifacts[0].ID {
		t.Fatalf("recovered delivery pagination=%#v err=%v", nextPage, err)
	}
	for _, artifact := range []map[string]any{before, after} {
		retention, intermediate, found, err := f.store.ArtifactCurrentPresentationState(stringValue(artifact["artifact_id"]))
		if err != nil || !found || retention != "snapshot" || intermediate {
			t.Fatalf("current delivery visibility differs: %q %t %t %v", retention, intermediate, found, err)
		}
		storedIntermediate, _, err := f.store.ArtifactVersionIntermediate(stringValue(artifact["version_id"]))
		if err != nil || !storedIntermediate {
			t.Fatal("history projection rewrote original draft provenance")
		}
	}
}

func TestRoundArtifactPresentationRejectsAbandonedBranchAndTracksUnavailableFiles(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	artifact := saveRoundPresentationFile(t, f, "branch-output", "result.txt", "retained history")
	finishRoundPresentation(t, f, "completed")
	if _, err := f.store.DeleteArtifactRealtime(context.Background(), stringValue(artifact["artifact_id"]), f.stream.OwnerID); err != nil {
		t.Fatal(err)
	}
	rounds, err := f.repo.CompletedRoundArtifactReferences(context.Background(), f.stream.UID, f.stream.OwnerID, []int64{f.claim.Attempt})
	if err != nil || len(rounds[f.claim.Attempt]) != 1 || rounds[f.claim.Attempt][0].Availability != transcriptstore.ArtifactDeleted {
		t.Fatalf("deleted file should remain explicitly unavailable: %#v %v", rounds, err)
	}
	forkRoundPresentationBeforeTerminal(t, f, rounds[f.claim.Attempt][0].SourceEventID)
	rounds, err = f.repo.CompletedRoundArtifactReferences(context.Background(), f.stream.UID, f.stream.OwnerID, []int64{f.claim.Attempt})
	if err != nil || len(rounds[f.claim.Attempt]) != 0 {
		t.Fatalf("abandoned terminal leaked attachments: %#v %v", rounds, err)
	}
}

func forkRoundPresentationBeforeTerminal(t *testing.T, f *agentSaveArtifactsFixture, source int64) {
	t.Helper()
	var parent string
	if err := f.db.QueryRow(`SELECT active_branch_id FROM transcript_branch_state WHERE stream_uid=?`, f.stream.UID).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := f.db.Exec(`INSERT INTO transcript_branches(
		stream_uid,branch_id,parent_branch_id,fork_event_id,fork_point,kind,
		client_mutation_id,request_sha256,source_message_id,created_at,updated_at
	) VALUES(?,?,?,?,?,'edit',?,zeroblob(32),?,?,?)`, f.stream.UID, "br_deadbeef", parent, source, 2,
		"round-artifact-branch-fixture", "save-user-message", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO transcript_branch_events(stream_uid,branch_id,ordinal,event_id)
		SELECT stream_uid,'br_deadbeef',ordinal,event_id FROM transcript_branch_events
		WHERE stream_uid=? AND branch_id=? AND event_id<=?`, f.stream.UID, parent, source); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE transcript_branch_state SET active_branch_id='br_deadbeef',generation=generation+1 WHERE stream_uid=?`, f.stream.UID); err != nil {
		t.Fatal(err)
	}
}
