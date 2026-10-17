package transcript

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"
)

func claimedComposerFixture(t *testing.T, legacy bool) (*Repository, *sql.DB, Stream, RunnerClaim) {
	t.Helper()
	repo, db, _ := newTranscriptRepository(t)
	if _, err := db.Exec(`INSERT INTO projects(id,user_id) VALUES('p','owner'); INSERT INTO frames(id,project_id,root_frame_id,status) VALUES('f','p','f','processing')`); err != nil {
		t.Fatal(err)
	}
	stream, err := repo.CreateStream(t.Context(), CreateStreamInput{UID: "frame:f", OwnerID: "owner", ExternalID: "f", SessionID: "f", Kind: StreamKindFrameRef, FrameID: "f", RootFrameID: "f", ProjectID: "p", Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if legacy {
		forceLegacyFrameAuthorityForTest(t, db, stream)
	}
	appendComposerFixtureInput(t, repo, stream, "selected", map[string]any{
		"inject_skills": []string{"personal-file-review"}, "files": []string{"fixture.csv"},
		"inject_mcp_server_ids": []string{"fixture-connector"},
	})
	claimed, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "runner", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	return repo, db, stream, claimed.Claim
}

func appendComposerFixtureInput(t *testing.T, repo *Repository, stream Stream, id string, input map[string]any) {
	t.Helper()
	if _, _, created, err := repo.AppendFrameUserEvent(t.Context(), AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: id, FrameEventID: id, MessageUUID: id,
		Text: "Review fixture data", MessageOrigin: "task_intent", InputData: input,
	}); err != nil || !created {
		t.Fatalf("append=%s created=%t err=%v", id, created, err)
	}
}

func assertClaimedComposerData(t *testing.T, repo *Repository, claim RunnerClaim, selected bool) {
	t.Helper()
	data, err := repo.ClaimedRunnerInputData(t.Context(), claim)
	if err != nil {
		t.Fatal(err)
	}
	if !selected {
		if len(data) != 0 {
			t.Fatalf("new ordinary input inherited prior composer data: %v", data)
		}
		return
	}
	raw, _ := json.Marshal(data)
	if string(raw) != `{"files":["fixture.csv"],"inject_mcp_server_ids":["fixture-connector"],"inject_skills":["personal-file-review"]}` {
		t.Fatalf("wrong claimed composer data: %s", raw)
	}
}

func TestClaimedRunnerInputDataExcludesNewerInputAndClearsNextTurn(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "canonical", true: "frame-reference"}[legacy], func(t *testing.T) {
			repo, _, stream, claim := claimedComposerFixture(t, legacy)
			appendComposerFixtureInput(t, repo, stream, "queued", nil)
			assertClaimedComposerData(t, repo, claim, true)
			if _, _, _, err := repo.FinishRunner(t.Context(), FinishRunnerInput{Claim: claim, ClientMessageID: "finished", Status: "completed", PayloadJSON: []byte(`{"text":"done"}`)}); err != nil {
				t.Fatal(err)
			}
			next, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "next", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
			if err != nil || !next.Claimed {
				t.Fatalf("next=%+v err=%v", next, err)
			}
			assertClaimedComposerData(t, repo, next.Claim, false)
		})
	}
}

func TestClaimedRunnerInputDataSurvivesControlResume(t *testing.T) {
	repo, _, stream, claim := claimedComposerFixture(t, false)
	checkpoint, _, _, err := repo.PauseRunnerForInput(t.Context(), AppendRunnerCheckpointInput{
		Claim: claim, ClientMessageID: "question", Phase: RunnerPhaseWaitingUser, Resumable: true,
		PayloadJSON: []byte(`{"status":"awaiting_user_response"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RunImmediate(t.Context(), func(tx *ImmediateTransaction) error {
		_, _, err := tx.AppendFrameInputResponse(t.Context(), AppendFrameInputResponseInput{
			StreamUID: stream.UID, OwnerID: stream.OwnerID, FrameID: stream.FrameID,
			ClientMessageID: "answer", PayloadJSON: []byte(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"question","content":"yes"}]}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	resumed, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "resumed", TTL: time.Minute, ResumeSource: ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence})
	if err != nil || !resumed.Claimed || resumed.Claim.ClaimedInputRevision <= claim.ClaimedInputRevision {
		t.Fatalf("resume=%+v err=%v", resumed, err)
	}
	assertClaimedComposerData(t, repo, resumed.Claim, true)
}

func TestClaimedRunnerInputDataRejectsStaleAuthority(t *testing.T) {
	for _, name := range []string{"owner", "claim-token", "input-revision", "expired-lease", "branch-generation", "epoch-transition-guard"} {
		t.Run(name, func(t *testing.T) {
			repo, db, stream, claim := claimedComposerFixture(t, false)
			switch name {
			case "owner":
				claim.OwnerID = "another-owner"
			case "claim-token":
				claim.ClaimToken = "another-claim"
			case "input-revision":
				claim.ClaimedInputRevision++
			case "expired-lease":
				expireAdmissionClaim(t, repo, claim)
			case "branch-generation":
				if _, err := db.Exec(`UPDATE transcript_branch_state SET generation=generation+1 WHERE stream_uid=?`, stream.UID); err != nil {
					t.Fatal(err)
				}
			case "epoch-transition-guard":
				if _, err := db.Exec(`UPDATE transcript_frame_authority SET active_epoch=active_epoch+1 WHERE owner_id=? AND session_id=?`, stream.OwnerID, stream.SessionID); err == nil {
					t.Fatal("storage accepted an invalid epoch transition")
				}
				assertClaimedComposerData(t, repo, claim, true)
				return
			}
			if data, err := repo.ClaimedRunnerInputData(t.Context(), claim); err == nil || data != nil {
				t.Fatalf("invalid authority exposed composer data: data=%v err=%v", data, err)
			}
		})
	}
}

func TestClaimedRunnerInputDataKeepsAnsweredBranchPrefixAndFencesOldRunner(t *testing.T) {
	repo, db, stream, claim := claimedComposerFixture(t, false)
	seedAskUserBranchFacts(t, repo, db, stream, "ask_user")
	base, err := repo.GetBranchState(t.Context(), stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.ForkFrameAskUserAnswerBranch(t.Context(), ForkFrameAskUserAnswerBranchInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, SourceBranchID: base.ActiveBranchID,
		ExpectedActiveBranchID: base.ActiveBranchID, ExpectedGeneration: base.Generation,
		ClientMutationID: "answer-branch", ToolUseID: "ask-1", SourceMessageIndex: 1,
		Response: AskUserBranchResponse{Action: "decide_for_me"}, Destinations: []string{"ws"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ClaimedRunnerInputData(t.Context(), claim); err == nil {
		t.Fatal("branch mutation left the old runner authorized")
	}
	// An answer is control input, not permission for a generic fresh worker.
	// Model the workspace dispatch owner's claimed resume event at this
	// repository boundary, then use its existing user-input resume contract.
	if _, err := db.Exec(`INSERT INTO frame_events(id,frame_id,sequence,event_type,payload,created_at)
		SELECT 'answer-resume',?,COALESCE(MAX(sequence),0)+1,'frame_resumed','{"dispatch":{"status":"claimed"}}',?
		FROM frame_events WHERE frame_id=?`, stream.FrameID, repo.now().UTC(), stream.FrameID); err != nil {
		t.Fatal(err)
	}
	resumed, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
		RunnerID: "frame-resume:answer-resume", TTL: time.Minute, ResumeSource: ResumeSourceUserInput})
	if err != nil || !resumed.Claimed {
		t.Fatalf("branch claim=%+v err=%v", resumed, err)
	}
	assertClaimedComposerData(t, repo, resumed.Claim, true)
}
