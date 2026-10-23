package transcript

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

const claimedDiscoveryTool = "list_host_grants"

func appendClaimedDiscoveryCheckpoint(t *testing.T, repo *Repository, claim RunnerClaim, id string, raw []byte) Event {
	t.Helper()
	_, event, created, err := repo.AppendRunnerCheckpoint(t.Context(), AppendRunnerCheckpointInput{
		Claim: claim, ClientMessageID: id, Phase: RunnerPhaseExecuting, Resumable: true, PayloadJSON: raw,
	})
	if err != nil || !created {
		t.Fatalf("checkpoint %q created=%t err=%v", id, created, err)
	}
	return event
}

func requireClaimedDiscovery(t *testing.T, repo *Repository, claim RunnerClaim, event Event, raw []byte) ClaimedToolDiscovery {
	t.Helper()
	got, err := repo.ReadClaimedToolDiscovery(t.Context(), claim, claimedDiscoveryTool)
	if err != nil || got.Input == nil || got.Input.Association == nil || got.EventID != event.EventID ||
		got.PublicationSequence != event.PublicationSeq || !bytes.Equal(got.PayloadJSON, raw) {
		t.Fatalf("discovery=%+v raw=%s err=%v want original event=%d/%d raw=%s", got, got.PayloadJSON, err, event.EventID, event.PublicationSeq, raw)
	}
	association := got.Input.Association
	var incarnation string
	if err := repo.readDatabase().QueryRowContext(t.Context(), `SELECT incarnation_id FROM frames
		WHERE id=(SELECT frame_id FROM transcript_streams WHERE stream_uid=?)`, claim.StreamUID).Scan(&incarnation); err != nil {
		t.Fatal(err)
	}
	if association.Scope.StreamUID != claim.StreamUID || association.ClaimedInputRevision != claim.ClaimedInputRevision ||
		association.RunnerAttempt != claim.Attempt || association.Scope.FrameIncarnationID != incarnation {
		t.Fatalf("discovery lost live input association: %+v claim=%+v", association, claim)
	}
	return got
}

func TestReadClaimedToolDiscoveryKeepsClaimedInputAndLatestTerminal(t *testing.T) {
	repo, _, stream, claim := claimedComposerFixture(t, false)
	absent, err := repo.ReadClaimedToolDiscovery(t.Context(), claim, claimedDiscoveryTool)
	if err != nil || absent.Input == nil || absent.Input.ClientMessageID != "selected" || absent.EventID != 0 || len(absent.PayloadJSON) != 0 {
		t.Fatalf("absent discovery=%+v err=%v", absent, err)
	}
	// Keep the source's exact bytes, rather than reserializing a result map.
	raw := []byte("{\n  \"status\": \"running\", \"toolName\": \"list_host_grants\", \"toolPhase\": \"completed\",\n  \"toolCallId\": \"discovery-a\", \"toolInput\": {}, \"toolResult\": {\"grants\": [{\"path\": \"/synthetic/alpha\", \"mode\": \"ro\"}]}\n}")
	first := appendClaimedDiscoveryCheckpoint(t, repo, claim, "discovery-a", raw)
	before := requireClaimedDiscovery(t, repo, claim, first, raw)
	appendComposerFixtureInput(t, repo, stream, "queued-ordinary", map[string]any{"request": "Later ordinary request"})
	appendComposerFixtureInput(t, repo, stream, "queued-hidden", map[string]any{"turn_intent": "save-as-skill", "request": "Later hidden request"})
	after := requireClaimedDiscovery(t, repo, claim, first, raw)
	if after.Input.EventID != before.Input.EventID || after.Input.PublicationSeq != before.Input.PublicationSeq ||
		after.Input.ClientMessageID != "selected" || after.Input.InputData["turn_intent"] != nil ||
		after.Input.Association.InputRevision != claim.ClaimedInputRevision {
		t.Fatalf("queued input replaced current source: before=%+v after=%+v", before.Input, after.Input)
	}
	appendClaimedDiscoveryCheckpoint(t, repo, claim, "discovery-later-start", []byte(`{"status":"running","toolName":"list_host_grants","toolPhase":"start","toolCallId":"discovery-failed"}`))
	requireClaimedDiscovery(t, repo, claim, first, raw)
	failedRaw := []byte(`{"status":"running","toolName":"list_host_grants","toolPhase":"failed","toolCallId":"discovery-failed","toolResult":{"error":"fixture unavailable"}}`)
	failed := appendClaimedDiscoveryCheckpoint(t, repo, claim, "discovery-failed", failedRaw)
	// The repository deliberately returns the latest terminal failure. The
	// server owns classification; it must not receive an older success instead.
	requireClaimedDiscovery(t, repo, claim, failed, failedRaw)
	if _, _, _, err := repo.FinishRunner(t.Context(), FinishRunnerInput{Claim: claim, ClientMessageID: "discovery-first-finished", Status: "completed", PayloadJSON: []byte(`{"text":"done"}`)}); err != nil {
		t.Fatal(err)
	}
	next, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "hidden-owner", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
	if err != nil || !next.Claimed {
		t.Fatalf("next claim=%+v err=%v", next, err)
	}
	hidden := requireClaimedDiscovery(t, repo, next.Claim, failed, failedRaw)
	if hidden.Input.ClientMessageID != "queued-hidden" || hidden.Input.InputData["turn_intent"] != "save-as-skill" {
		t.Fatalf("repository hid the actual source from the server's control-input filter: %+v", hidden.Input)
	}
}

func TestReadClaimedToolDiscoveryIgnoresReplayWindowAndUsesVisibleBranch(t *testing.T) {
	repo, _, stream, claim := claimedComposerFixture(t, false)
	prefixRaw := []byte(`{"status":"running","toolName":"list_host_grants","toolPhase":"completed","toolCallId":"prefix-discovery","toolResult":{"grants":[]}}`)
	prefix := appendClaimedDiscoveryCheckpoint(t, repo, claim, "prefix-discovery", prefixRaw)
	appendClaimedDiscoveryCheckpoint(t, repo, claim, "unrelated-latest-checkpoint", []byte(`{"status":"running","message":"unrelated recent inspection"}`))
	// This is a real bounded replay-window contract, not a claim that a full
	// manual compaction service/browser workflow has executed in this fixture.
	replay, err := repo.ListRunnerReplay(t.Context(), ListRunnerReplayInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, MessageLimit: 1, CheckpointLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range replay {
		if event.Event.EventID == prefix.EventID {
			t.Fatal("fixture did not exclude the discovery from its small provider replay window")
		}
	}
	requireClaimedDiscovery(t, repo, claim, prefix, prefixRaw)
	if _, _, _, err := repo.FinishRunner(t.Context(), FinishRunnerInput{Claim: claim, ClientMessageID: "prefix-finished", Status: "completed", PayloadJSON: []byte(`{"text":"done"}`)}); err != nil {
		t.Fatal(err)
	}
	source, _, created, err := repo.AppendFrameUserEvent(t.Context(), AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "replace-source", FrameEventID: "replace-source-event", MessageUUID: "replace-source", Text: "Inspect the next synthetic input.",
	})
	if err != nil || !created {
		t.Fatalf("source created=%t err=%v", created, err)
	}
	sibling, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "sibling-owner", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
	if err != nil || !sibling.Claimed {
		t.Fatalf("sibling claim=%+v err=%v", sibling, err)
	}
	siblingRaw := []byte(`{"status":"running","toolName":"list_host_grants","toolPhase":"completed","toolCallId":"sibling-discovery","toolResult":{"grants":[{"path":"/synthetic/sibling-only","mode":"rw"}]}}`)
	siblingEvent := appendClaimedDiscoveryCheckpoint(t, repo, sibling.Claim, "sibling-discovery", siblingRaw)
	requireClaimedDiscovery(t, repo, sibling.Claim, siblingEvent, siblingRaw)
	base, err := repo.GetBranchState(t.Context(), stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	var sourceIndex int64
	if err := repo.RunImmediate(t.Context(), func(tx *ImmediateTransaction) error {
		var err error
		sourceIndex, err = branchWebMessageIndexConn(t.Context(), tx.conn, stream.UID, base.ActiveBranchID, source.EventID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	forked, err := repo.ForkFrameUserMessageBranch(t.Context(), ForkFrameUserMessageBranchInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, SourceBranchID: base.ActiveBranchID,
		ExpectedActiveBranchID: base.ActiveBranchID, ExpectedGeneration: base.Generation,
		ClientMutationID: "discovery-replacement", SourceClientMessageID: source.ClientMessageID, SourceMessageIndex: sourceIndex,
		ReplacementText: "Inspect the corrected synthetic input.", Destinations: []string{"ws"},
	})
	if err != nil || !forked.Created || forked.Generation != base.Generation+1 {
		t.Fatalf("fork=%+v err=%v", forked, err)
	}
	if got, err := repo.ReadClaimedToolDiscovery(t.Context(), sibling.Claim, claimedDiscoveryTool); err == nil || got.Input != nil || len(got.PayloadJSON) != 0 {
		t.Fatalf("old branch claim still exposed discovery: got=%+v err=%v", got, err)
	}
	child, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "child-owner", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
	if err != nil || !child.Claimed {
		t.Fatalf("child claim=%+v err=%v", child, err)
	}
	visible := requireClaimedDiscovery(t, repo, child.Claim, prefix, prefixRaw)
	if visible.Input.EventID != forked.ReplacementEvent.EventID || visible.Input.Association.Scope.BranchID != forked.BranchID ||
		visible.Input.Association.Scope.BranchGeneration != forked.Generation || visible.EventID == siblingEvent.EventID {
		t.Fatalf("shared prefix or sibling isolation failed: discovery=%+v fork=%+v", visible, forked)
	}
}

func TestReadClaimedToolDiscoveryRejectsStaleAuthority(t *testing.T) {
	for _, name := range []string{"owner", "claim-token", "input-revision", "expired-lease", "branch-generation", "inactive-higher-epoch"} {
		t.Run(name, func(t *testing.T) {
			repo, db, stream, claim := claimedComposerFixture(t, false)
			raw := []byte(`{"status":"running","toolName":"list_host_grants","toolPhase":"completed","toolCallId":"authority-discovery","toolResult":{"grants":[]}}`)
			event := appendClaimedDiscoveryCheckpoint(t, repo, claim, "authority-discovery", raw)
			requireClaimedDiscovery(t, repo, claim, event, raw)
			want := ErrClaimStale
			switch name {
			case "owner":
				claim.OwnerID = "foreign-owner"
				want = ErrOwnerMismatch
			case "claim-token":
				claim.ClaimToken = "not-the-current-token"
			case "input-revision":
				claim.ClaimedInputRevision++
			case "expired-lease":
				expireAdmissionClaim(t, repo, claim)
			case "branch-generation":
				// Negative authority invalidation, matching the existing claimed-
				// input test. Normal input admission above uses only typed APIs.
				if _, err := db.Exec(`UPDATE transcript_branch_state SET generation=generation+1 WHERE stream_uid=?`, stream.UID); err != nil {
					t.Fatal(err)
				}
				want = ErrEventConflict
			case "inactive-higher-epoch":
				// A real second stream is not automatically the serving epoch.
				// Do not fabricate a normal input or bypass the activation guard.
				inactive, err := repo.CreateStream(t.Context(), CreateStreamInput{
					UID: "inactive-discovery-epoch", OwnerID: stream.OwnerID, ExternalID: stream.ExternalID,
					SessionID: stream.SessionID, Kind: StreamKindFrameRef, ProjectID: stream.ProjectID,
					RootFrameID: stream.RootFrameID, FrameID: stream.FrameID, Epoch: stream.Epoch + 1,
				})
				if err != nil {
					t.Fatal(err)
				}
				requireClaimedDiscovery(t, repo, claim, event, raw)
				claim.StreamUID = inactive.UID
				want = ErrEventConflict
			}
			got, err := repo.ReadClaimedToolDiscovery(t.Context(), claim, claimedDiscoveryTool)
			if !errors.Is(err, want) || got.Input != nil || got.EventID != 0 || len(got.PayloadJSON) != 0 {
				t.Fatalf("invalid authority leaked discovery: got=%+v err=%v want=%v", got, err, want)
			}
		})
	}
}
