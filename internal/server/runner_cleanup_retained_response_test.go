package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestRunnerCleanupFailureRetainsAcceptedResponseInLiveAndHistory(t *testing.T) {
	const answer = "  The accepted result remains available.\n\nRecorded values: 2, 4, 8.\n"
	const cleanupSecret = "private kernel cleanup detail"
	for _, test := range []struct {
		name, answer, status string
		chatError            error
		stop                 bool
		retain               bool
	}{
		{name: "accepted reply", answer: answer, status: "failed", retain: true},
		{name: "no reply", status: "failed"},
		{name: "rejected reply", answer: answer, status: "failed", chatError: errors.New("synthetic response rejected")},
		{name: "user cancellation", answer: answer, status: "cancelled", stop: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			fixture := newTranscriptWebReadModelServingFixture(t, 1)
			claim, err := fixture.repository.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
				StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, RunnerID: "cleanup-fixture",
				TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
			})
			if err != nil || !claim.Claimed {
				t.Fatalf("claim=%#v err=%v", claim, err)
			}
			authority := &transcriptRunnerAuthority{Stream: fixture.stream, Claim: claim.Claim}
			run := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID, Attempt: int(claim.Claim.Attempt), Transcript: authority}
			if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
				t.Fatal(err)
			}
			result := &SessionRunnerCycleResult{SessionID: run.SessionID, Attempt: run.Attempt}
			if test.stop {
				cancel(ErrGenerationStopped)
			}
			var cleanupCalls int
			err = fixture.server.settleSessionRunnerChatOutcomeWithCleanup(ctx, SessionRunnerChatOptions{RunnerID: claim.Claim.RunnerID},
				result, &activeSessionRun{}, sessionstore.RunnerMutationClaim{}, authority, run,
				sessionstore.Session{ID: run.SessionID}, nil, test.answer, test.chatError, "",
				func(_ context.Context, sessionID string, actual *transcriptRunnerAuthority) error {
					if sessionID != run.SessionID || actual != authority {
						t.Fatal("cleanup was called with a different runtime owner")
					}
					cleanupCalls++
					return errors.New(cleanupSecret)
				})
			if err != nil || cleanupCalls != 1 || result.Status != test.status {
				t.Fatalf("settlement calls=%d status=%q err=%v", cleanupCalls, result.Status, err)
			}
			if projection, err := fixture.repository.GetTerminalProjection(t.Context(), fixture.stream.OwnerID, fixture.stream.UID, result.FinishEventID); err != nil ||
				projection.TerminalStatus != test.status || (!test.stop && projection.ReasonCode != "runner_cleanup_failed") {
				t.Fatalf("terminal projection=%#v err=%v", projection, err)
			}
			if _, err := fixture.repository.GetTerminalProjection(t.Context(), "foreign-owner", fixture.stream.UID, result.FinishEventID); !errors.Is(err, transcriptstore.ErrOwnerMismatch) {
				t.Fatalf("foreign owner terminal error=%v", err)
			}
			assistant, found, err := fixture.repository.GetEventByClientMessageID(t.Context(), fixture.stream.UID, fixture.stream.OwnerID,
				transcriptRunnerClientMessageID(claim.Claim, "assistant-message"))
			if err != nil || found != test.retain {
				t.Fatalf("durable accepted reply found=%t want=%t err=%v", found, test.retain, err)
			}
			if !test.retain {
				return
			}
			if assistant.EventID >= result.FinishEventID || !strings.Contains(string(assistant.PayloadJSON), "The accepted result remains available.") {
				t.Fatalf("retained assistant is absent or ordered after terminal: event=%d terminal=%d", assistant.EventID, result.FinishEventID)
			}
			assertRetainedResponseProjectionRollback(t, fixture)
			if err := fixture.server.runTranscriptWebReadModelCycle(t.Context()); err != nil {
				t.Fatal(err)
			}
			response := p3JSONRequest(t, fixture.server, http.MethodGet, "/api/conversations/"+fixture.stream.SessionID+"/messages", nil, "")
			if response.Code != http.StatusOK || strings.Contains(response.Body.String(), cleanupSecret) {
				t.Fatalf("history status=%d body=%s", response.Code, response.Body.String())
			}
			items := transcriptWebServingItems(t, p3DecodeObject(t, response), "items")
			if len(items) != 3 {
				t.Fatalf("history items=%#v", items)
			}
			body, diagnostic := items[1].(map[string]any), items[2].(map[string]any)
			diagnosticBody := webString(mapValue(diagnostic["content"])["content"])
			if got := webString(mapValue(body["content"])["content"]); got != answer || body["status"] != "finish" ||
				diagnosticBody == "" || diagnosticBody == answer || diagnostic["terminal_status"] != "failed" || diagnostic["id"] == body["id"] {
				t.Fatalf("separate retained response and failure were not replayed: body=%#v diagnostic=%#v", body, diagnostic)
			}
			basePath := "/api/conversations/" + fixture.stream.SessionID + "/messages"
			noticePage := p3JSONRequest(t, fixture.server, http.MethodGet, basePath+"?limit=1", nil, "")
			requireTranscriptWebServingPageIDs(t, noticePage, webString(diagnostic["id"]))
			cursor := webString(p3DecodeObject(t, noticePage)["oldest_cursor"])
			answerPage := p3JSONRequest(t, fixture.server, http.MethodGet, basePath+"?limit=1&before="+url.QueryEscape(cursor), nil, "")
			requireTranscriptWebServingPageIDs(t, answerPage, webString(body["id"]))
			answerItem := transcriptWebServingItems(t, p3DecodeObject(t, answerPage), "items")[0].(map[string]any)
			if got := webString(mapValue(answerItem["content"])["content"]); got != answer {
				t.Fatalf("separately paginated answer=%q want=%q", got, answer)
			}
			full, found, err := fixture.server.loadTranscriptWebHistory(t.Context(), fixture.stream.OwnerID, fixture.stream.SessionID)
			if err != nil || !found || len(full) != 3 || full[1]["id"] != body["id"] || full[2]["id"] != diagnostic["id"] {
				t.Fatalf("full projection did not match incremental identities: found=%t messages=%#v err=%v", found, full, err)
			}
			if err := fixture.server.drainTranscriptWebDeliveries(t.Context()); err != nil {
				t.Fatal(err)
			}
			var liveBody, liveDiagnostic bool
			for _, event := range transcriptWebEvents(t, fixture.store, fixture.stream.OwnerID) {
				if event.Type != "message.stream" {
					continue
				}
				payload := event.Payload
				switch payload["stream_type"] {
				case "content":
					liveBody = payload["msg_id"] == body["id"] && payload["data"] == answer
				case "error":
					liveDiagnostic = payload["msg_id"] == diagnostic["id"] && payload["terminal_status"] == "failed" &&
						payload["data"] == diagnosticBody
				}
			}
			if !liveBody || !liveDiagnostic {
				t.Fatalf("live reply=%t cleanup diagnostic=%t", liveBody, liveDiagnostic)
			}
			assertRetainedResponseReopenAndResume(t, fixture, claim.Claim, answer, diagnosticBody, webString(body["id"]), webString(diagnostic["id"]))
			assertRetainedResponseMarkerBinding(t, claim.Claim)
		})
	}
}

func assertRetainedResponseProjectionRollback(t *testing.T, f transcriptWebReadModelServingFixture) {
	t.Helper()
	const rejectNotice = `CREATE TRIGGER reject_cleanup_notice_probe BEFORE INSERT ON transcript_web_messages
		WHEN json_extract(NEW.message_json, '$.terminal_reason_code') = 'runner_cleanup_failed'
		BEGIN SELECT RAISE(ABORT, 'synthetic cleanup notice projection rejection'); END`
	if _, err := f.db.Exec(rejectNotice); err != nil {
		t.Fatal(err)
	}
	work := requireSingleTranscriptWebProjectionWork(t, f.readModel, f.stream.OwnerID)
	if _, err := f.server.tryIncrementalTranscriptWebReadModel(t.Context(), f.stream, work); err == nil ||
		!strings.Contains(err.Error(), "synthetic cleanup notice projection rejection") {
		t.Fatalf("expected rollback at notice write: %v", err)
	}
	state, messages, found, err := f.readModel.GetTranscriptWebProjectionCheckpoint(t.Context(), f.stream.OwnerID, f.stream.UID, work.BranchID)
	if err != nil || !found || len(messages) != 1 || state.ProjectionRevision != f.fence.StateProjectionRevision {
		t.Fatalf("failed notice left a partial checkpoint: found=%t messages=%d revision=%d err=%v",
			found, len(messages), state.ProjectionRevision, err)
	}
	if _, err := f.db.Exec("DROP TRIGGER reject_cleanup_notice_probe"); err != nil {
		t.Fatal(err)
	}
	retried, err := f.server.tryIncrementalTranscriptWebReadModel(t.Context(), f.stream, work)
	if err != nil || retried.Disposition != transcriptWebIncrementalAppliedReady || retried.MessagesWritten != 2 {
		t.Fatalf("same checkpoint work did not publish one reply and one notice: result=%#v err=%v", retried, err)
	}
}

func assertRetainedResponseReopenAndResume(t *testing.T, f transcriptWebReadModelServingFixture, original transcriptstore.RunnerClaim,
	answer, diagnostic, answerID, diagnosticID string,
) {
	t.Helper()
	var sequence int
	var name, path string
	if err := f.db.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil || name != "main" || path == "" {
		t.Fatalf("fixture database path: %v", err)
	}
	reopened, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	server, readModel, _ := newTranscriptWebReadModelServingServer(t, f.store, f.repository, reopened, f.stream)
	base := "/api/conversations/" + f.stream.SessionID + "/messages"
	reopenedPage := p3JSONRequest(t, server, http.MethodGet, base+"?limit=2", nil, "")
	requireTranscriptWebServingPageIDs(t, reopenedPage, answerID, diagnosticID)
	assertBodyAndNotice := func(responseItems []any, superseded bool) {
		t.Helper()
		body, tip := responseItems[0].(map[string]any), responseItems[1].(map[string]any)
		content := mapValue(tip["content"])
		tipType, status := "error", "error"
		if superseded {
			tipType, status = "warning", "finish"
		}
		if mapValue(body["content"])["content"] != answer || body["status"] != "finish" ||
			content["content"] != diagnostic || content["type"] != tipType || tip["status"] != status ||
			(tip["terminal_superseded"] == true) != superseded {
			t.Fatalf("reopened or resumed projections lost the accepted reply or failure notice: body=%#v notice=%#v", body, tip)
		}
	}
	assertBodyAndNotice(transcriptWebServingItems(t, p3DecodeObject(t, reopenedPage), "items"), false)
	ctx := t.Context()
	resume, err := f.store.ResumeCompatibilityFrameConversation(f.stream.RootFrameID, workspace.ResumeCompatibilityFrameInput{})
	if err != nil || resume.Event == nil {
		t.Fatalf("resume=%#v err=%v", resume, err)
	}
	dispatch, claimed, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("cleanup-resume-fixture", time.Minute)
	if err != nil || !claimed || dispatch.ResumeEvent.ID != resume.Event.ID {
		t.Fatalf("dispatch=%#v claimed=%t err=%v", dispatch, claimed, err)
	}
	retry, err := f.repository.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: "frame-resume:" + dispatch.ResumeEvent.ID,
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceUserInput,
	})
	if err != nil || !retry.Claimed || retry.Claim.ClaimedInputRevision != original.ClaimedInputRevision {
		t.Fatalf("same-input retry=%#v err=%v", retry, err)
	}
	segment := transcriptstore.AssistantSegmentPayloadV1(1, "")
	appendRunnerPayloadEvent(t, f.repository, retry.Claim, "cleanup-resumed-answer", "assistant_message",
		map[string]any{"text": "Recovered answer.", "assistant_segment": segment})
	if _, _, created, err := f.repository.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
		Claim: retry.Claim, ClientMessageID: "cleanup-resumed-terminal", Status: "completed",
		PayloadJSON: publicAdvisoryProjectionJSON(t, map[string]any{"status": "completed", "assistant_segment": segment}),
	}); err != nil || !created {
		t.Fatalf("retry terminal created=%t err=%v", created, err)
	}
	work := requireSingleTranscriptWebProjectionWork(t, readModel, f.stream.OwnerID)
	incremental, err := server.tryIncrementalTranscriptWebReadModel(ctx, f.stream, work)
	if err != nil || incremental.Disposition != transcriptWebIncrementalFallbackAssistant {
		t.Fatalf("same-input supersession must rebuild historical presentation: result=%#v err=%v", incremental, err)
	}
	if err := server.rebuildTranscriptWebReadModel(ctx, work); err != nil {
		t.Fatal(err)
	}
	page := p3JSONRequest(t, server, http.MethodGet, base+"?limit=3", nil, "")
	retryID := transcriptAssistantMessageID(f.stream.SessionID, retry.Claim.Attempt, 1)
	requireTranscriptWebServingPageIDs(t, page, answerID, diagnosticID, retryID)
	assertBodyAndNotice(transcriptWebServingItems(t, p3DecodeObject(t, page), "items"), true)
}

func assertRetainedResponseMarkerBinding(t *testing.T, claim transcriptstore.RunnerClaim) {
	t.Helper()
	payload := func(v map[string]any) []byte {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	segment := transcriptstore.AssistantSegmentPayloadV1(1, "")
	assistant := transcriptstore.AppendEventInput{
		Claim: claim, ClientMessageID: "accepted-assistant", Type: "assistant_message", Source: transcriptstore.EventSourcePayload,
		PayloadJSON: payload(map[string]any{"text": "Accepted.", "assistant_segment": segment}),
	}
	finish := map[string]any{"status": "failed", "reason_code": transcriptstore.RunnerCleanupFailedReason,
		"retained_final_response_id": assistant.ClientMessageID, "assistant_segment": segment}
	input := transcriptstore.RunnerFinalizationInput{Assistant: &assistant, Finish: transcriptstore.FinishRunnerInput{
		Claim: claim, ClientMessageID: "accepted-assistant-terminal", Status: "failed", PayloadJSON: payload(finish),
	}}
	if err := transcriptstore.ValidateRunnerFinalizationInput(input); err != nil {
		t.Fatalf("valid cleanup binding rejected: %v", err)
	}
	for _, mutation := range []struct {
		key string
		bad any
	}{{"retained_final_response_id", "foreign-assistant"}, {"assistant_segment", transcriptstore.AssistantSegmentPayloadV1(2, "")},
		{"reason_code", "runner_execution_failed"}} {
		prior := finish[mutation.key]
		finish[mutation.key] = mutation.bad
		input.Finish.PayloadJSON = payload(finish)
		if err := transcriptstore.ValidateRunnerFinalizationInput(input); !errors.Is(err, transcriptstore.ErrEventConflict) {
			t.Fatalf("invalid cleanup binding %s error=%v", mutation.key, err)
		}
		finish[mutation.key] = prior
	}
}
