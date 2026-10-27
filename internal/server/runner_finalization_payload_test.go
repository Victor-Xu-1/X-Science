package server

import (
	"context"
	"strings"
	"testing"

	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestRunnerFinalizationRejectedAssistantPayloadPersistsFailedTerminal(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	f.server.memoryExtraction = nil
	authority := &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), Transcript: authority, ResponseLanguage: "en"}
	if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
		t.Fatal(err)
	}
	active := &activeSessionRun{}
	result := &SessionRunnerCycleResult{SessionID: run.SessionID, Attempt: run.Attempt}
	err := f.server.settleSessionRunnerChatOutcome(context.Background(), SessionRunnerChatOptions{RunnerID: f.claim.RunnerID}, result, active,
		sessionstore.RunnerMutationClaim{}, authority, run, sessionstore.Session{ID: run.SessionID}, nil, strings.Repeat("x", transcriptstore.MaxEventPayloadBytes+1), nil, "")
	if err != nil || result.Status != "failed" || result.FinishEventID == 0 || !active.settled {
		t.Fatalf("deterministic payload failure did not settle: result=%#v settled=%t err=%v", result, active.settled, err)
	}
	projection, err := f.repo.GetTerminalProjection(t.Context(), f.stream.OwnerID, f.stream.UID, result.FinishEventID)
	if err != nil || projection.TerminalStatus != "failed" || projection.ReasonCode != "runner_response_persistence_failed" {
		t.Fatalf("original failure reason lost: projection=%#v err=%v", projection, err)
	}
	for kind, want := range map[string]int{"assistant_message": 0, "runner_finished": 1} {
		var count int
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type=?`, f.stream.UID, kind).Scan(&count); err != nil || count != want {
			t.Fatalf("kind=%s count=%d want=%d err=%v", kind, count, want, err)
		}
	}
}
