package server

import (
	"context"
	"fmt"
	"testing"

	eventjournal "synon-go/internal/persistence/journal"
	sessionstore "synon-go/internal/persistence/sessions"
)

func TestLanguageRecoveryParksUnchangedProviderOnlyLoopAfterDatabaseReopen(t *testing.T) {
	fixture := newCorrectionRouteFixture(t)
	failure := sessionRunnerResponseLanguageMismatch{ValidationCode: "output_language"}
	for cycle := 0; cycle <= sessionRunnerConsecutiveIdenticalToolRoundBudget; cycle++ {
		claim := fixture.run.Transcript.Claim
		appendRunnerToolCheckpoint(t, fixture.repo, claim, fmt.Sprintf("language-provider-%d", cycle), map[string]any{
			"status": "running", "stage": "model_execution", "lifecyclePhase": "provider",
		})
		entries, err := fixture.server.loadTranscriptRunnerReplay(context.Background(), fixture.run.Transcript, 20, 20)
		if err != nil {
			t.Fatal(err)
		}
		result := &SessionRunnerCycleResult{SessionID: fixture.run.SessionID, Attempt: fixture.run.Attempt}
		var callErr error = failure
		handled, err := fixture.server.handleSessionRunnerChatInterruption(context.Background(),
			SessionRunnerChatOptions{SessionID: fixture.run.SessionID, RunnerID: claim.RunnerID}, result,
			&activeSessionRun{}, sessionstore.RunnerMutationClaim{}, fixture.run.Transcript, fixture.run, entries, &callErr, nil)
		if err != nil || !handled || result.Status != "interrupted" {
			t.Fatalf("cycle=%d result=%#v handled=%t error=%v", cycle, result, handled, err)
		}
		park := cycle == sessionRunnerConsecutiveIdenticalToolRoundBudget
		if result.AwaitingRecoveryCondition != park || result.InterruptionAutoResume == park {
			t.Fatalf("unchanged model-only language loop: cycle=%d result=%#v want park=%t", cycle, result, park)
		}
		if park {
			if _, eligible, err := fixture.repo.GetAutoResumeCandidate(context.Background(), fixture.stream.UID, fixture.stream.OwnerID); err != nil || eligible {
				t.Fatalf("parked language loop still schedules: eligible=%t error=%v", eligible, err)
			}
			if _, resumable, err := fixture.repo.LatestResumableCheckpoint(context.Background(), fixture.stream.UID, fixture.stream.OwnerID); err != nil || !resumable {
				t.Fatalf("logical task lost continuation: resumable=%t error=%v", resumable, err)
			}
			break
		}
		if cycle == 1 {
			fixture.reopen(t)
		}
		fixture.resume(t)
	}
}

func TestLanguageRecoveryDoesNotCountPreparationForeignPayloadOrHealthyWork(t *testing.T) {
	cause := sessionRunnerResponseLanguageMismatch{}.runnerCorrection()
	for _, scenario := range []string{"preparation", "foreign-payload", "not-executing", "material"} {
		t.Run(scenario, func(t *testing.T) {
			state := runnerCorrectionRepetition{}
			for cycle := 0; cycle < 10; cycle++ {
				entry := eventjournal.Entry{SourceEventType: "runner_checkpoint", Message: eventjournal.Message{
					"type": "runner_checkpoint", "status": "running", "stage": "model_execution", "lifecyclePhase": "provider",
				}}
				switch scenario {
				case "preparation":
					entry.Message["stage"] = "history_replay"
				case "foreign-payload":
					entry.SourceEventType = "tool_completed"
				case "not-executing":
					entry.Message["status"] = "pending"
				}
				state.observe(entry)
				if scenario == "material" {
					state.observe(eventjournal.Entry{SourceEventType: "runner_checkpoint", Message: eventjournal.Message{
						"type": "runner_checkpoint", "status": "completed", "toolPhase": "completed", "toolName": "python",
						"toolResult": map[string]any{"ok": true, "executed": true, "stdout": fmt.Sprint(cycle)},
					}})
				}
				state.observe(eventjournal.Entry{SourceEventType: "runner_checkpoint", RuntimeProjection: cause, Message: eventjournal.Message{
					"type": "runner_checkpoint", "status": "interrupted", "reason_code": cause.ReasonCode,
					"recovery_contract_revision": sessionRunnerRecoveryContractRevision,
				}})
			}
			if state.waitsForChangedCondition(cause) {
				t.Fatalf("%s spuriously quarantined the language route: %#v", scenario, state)
			}
		})
	}
}
