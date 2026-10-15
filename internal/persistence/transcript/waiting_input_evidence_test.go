package transcript

import "testing"

func TestFreshMessageEvidenceKeepsWaitingAnswersOnCurrentCheckpoint(t *testing.T) {
	for _, scenario := range []string{"waiting_user_answer", "waiting_approval_answer", "explicit_new_task", "interrupted_response", "unmapped_prefix_response", "response_before_pause", "mixed_new_task"} {
		t.Run(scenario, func(t *testing.T) {
			repo, db, stream, claim := admissionFrameFixture(t)
			appendMessage := func(id, origin string) {
				t.Helper()
				if _, _, created, err := repo.AppendFrameUserEvent(t.Context(), AppendFrameUserEventInput{
					StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: id,
					FrameEventID: id + "-frame", MessageUUID: id + "-message", Text: id, MessageOrigin: origin,
				}); err != nil || !created {
					t.Fatalf("append %s created=%t err=%v", id, created, err)
				}
			}
			if scenario == "response_before_pause" {
				appendMessage("early", "input_response")
			}
			pause := AppendRunnerCheckpointInput{Claim: claim.Claim, ClientMessageID: "waiting", Phase: RunnerPhaseWaitingUser,
				Resumable: true, PayloadJSON: []byte(`{"status":"awaiting_user_response"}`)}
			switch scenario {
			case "interrupted_response":
				if _, err := repo.InterruptRunner(t.Context(), InterruptRunnerInput{Claim: claim.Claim, ClientMessageID: "interrupted",
					ReasonCode: "model_provider_unavailable", Resumable: true, AutoResume: true}); err != nil {
					t.Fatal(err)
				}
			case "waiting_approval_answer":
				pause.Phase = RunnerPhaseWaitingApproval
				pause.PayloadJSON = []byte(`{"status":"awaiting_approval"}`)
				if _, _, _, err := repo.PauseRunnerForApproval(t.Context(), pause); err != nil {
					t.Fatal(err)
				}
			default:
				if _, _, _, err := repo.PauseRunnerForInput(t.Context(), pause); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "unmapped_prefix_response" {
				appendMessage("unknown-prefix", "input_response")
				// Controlled historical-gap fixture: remove one mapping, not its
				// original event or counter. A later exact source must not cause
				// the reader to pretend that this missing prefix is complete.
				if _, err := db.Exec(`DROP TRIGGER transcript_input_admissions_retained`); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`DELETE FROM transcript_input_admissions WHERE stream_uid=? AND input_revision=2`, stream.UID); err != nil {
					t.Fatal(err)
				}
			}
			origin := "input_response"
			if scenario == "explicit_new_task" {
				origin = "task_intent"
			}
			appendMessage("later", origin)
			if scenario == "mixed_new_task" {
				appendMessage("new-task", "task_intent")
			}
			evidence, err := repo.GetFreshMessageEvidence(t.Context(), stream.UID, stream.OwnerID)
			wantFresh := scenario != "waiting_user_answer" && scenario != "waiting_approval_answer"
			if err != nil || evidence.Authorized != wantFresh || !evidence.Coverage.HasMessage ||
				evidence.Coverage.Complete != (scenario != "unmapped_prefix_response") {
				t.Fatalf("evidence=%+v want fresh=%t err=%v", evidence, wantFresh, err)
			}
			if wantFresh && evidence.Source != "exact_admission" {
				t.Fatalf("fresh source was not exact admission: %+v", evidence)
			}
		})
	}
}
