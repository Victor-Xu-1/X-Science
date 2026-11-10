package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

// Variant of TestSemanticReviewResumeUsesCurrentChecksWithoutForcedTools.
// No tool batch, download, scientific execution or completed answer is seeded.
func TestTemplateQualityCancelledContinueUsesAdvisoryBeforeProvider(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	claimAnsweredRecoveryFixtureInput(t, f)
	if _, found, err := f.repo.EnsureActiveFrameTaskIntent(t.Context(), f.stream.UID, f.stream.OwnerID); err != nil || !found {
		t.Fatalf("canonical task intent: found=%t err=%v", found, err)
	}
	cause := (&sessionRunnerReferenceIntegrityError{CrossArtifactFailures: []string{"unresolved_template_marker:report.md"}}).runnerCorrection()
	if _, err := f.repo.InterruptRunner(t.Context(), transcriptstore.InterruptRunnerInput{
		Claim: f.claim, ClientMessageID: "prior-template-condition", ReasonCode: cause.ReasonCode,
		ResumeDetail: cause.Detail, Cause: &cause, Resumable: true, AutoResume: true,
		RecoveryContractRevision: sessionRunnerRecoveryContractRevision,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.CancelFrameWithTranscript(t.Context(), f.stream.FrameID); err != nil {
		t.Fatal(err)
	}
	target, err := f.store.GetCompatibilityFrameResumeTarget(t.Context(), f.stream.OwnerID, f.stream.FrameID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ResumeCompatibilityFrameConversation(f.stream.FrameID, workspace.ResumeCompatibilityFrameInput{Command: &workspace.CompatibilityFrameContinueCommand{
		ActorID: f.stream.OwnerID, ClientCommandID: "09ad1cb2-3708-4496-bd63-a213447ec346", Target: target,
	}}); err != nil {
		t.Fatal(err)
	}
	var lock sync.Mutex
	var observed []map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		if strings.Contains(string(mustJSON(t, request["messages"])), "Write a brief expert orientation before the task begins.") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"I will inspect the supplied task."}}]}`))
			return
		}
		lock.Lock()
		observed = append(observed, request)
		lock.Unlock()
		// A deliberate boundary stop verifies restored policy, not completion.
		http.Error(w, "controlled provider boundary stop", http.StatusBadRequest)
	}))
	t.Cleanup(provider.Close)
	result, runErr := f.server.RunFrameResumeDispatchOnce(t.Context(), FrameResumeDispatchOptions{WorkerID: "template-policy-resume", ClaimTTL: time.Minute,
		Chat: SessionRunnerChatOptions{Endpoint: provider.URL + "/v1/chat/completions", APIKey: "fixture", Model: "fixture",
			AllowedTools: []string{"read_file", "ask_user"}, LeaseTTL: time.Minute, MaxAttempts: 1, MaxToolRounds: 1,
			DisableSkillDiscovery: true, DisableMCPDiscovery: true}})
	lock.Lock()
	defer lock.Unlock()
	if len(observed) == 0 {
		t.Fatalf("resumed provider boundary absent: %#v err=%v", result, runErr)
	}
	for _, request := range observed {
		text := string(mustJSON(t, request["messages"]))
		if request["tool_choice"] == "required" || strings.Contains(text, sessionRunnerDurableCorrectionContextMarker) || strings.Contains(text, cause.Condition.ContentID()) {
			t.Error("old typed template quality finding reached the provider as a repair obligation")
		}
	}
	if runErr == nil && result.Status == "completed" {
		t.Fatal("advisory recovery fabricated task completion at the controlled boundary")
	}
}

func TestTemplateQualityTypedRecoveryKeepsHardFieldsAndRetiresReadAuthority(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reference transcriptstore.RunnerReferenceCondition
		advisory  bool
	}{
		{"template", transcriptstore.RunnerReferenceCondition{CrossArtifactFailures: []string{"unresolved_template_marker:report.md"}}, true},
		{"unknown", transcriptstore.RunnerReferenceCondition{CrossArtifactFailures: []string{"unresolved_template_marker:report.md", "unknown_integrity_failure:report.md"}}, false},
		{"malformed", transcriptstore.RunnerReferenceCondition{CrossArtifactFailures: []string{"unresolved_template_marker:report.md", "malformed_artifact_placeholder:report.md value=bad"}}, false},
		{"missing", transcriptstore.RunnerReferenceCondition{CrossArtifactFailures: []string{"unresolved_template_marker:report.md"}, MissingRequiredDeliverables: []string{"report.csv"}}, false},
		{"invalid", transcriptstore.RunnerReferenceCondition{CrossArtifactFailures: []string{"unresolved_template_marker:report.md"}, InvalidReferenceArtifacts: []string{"foreign version"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			cause := (&sessionRunnerReferenceIntegrityError{}).runnerCorrection()
			cause.Condition.Reference = &tc.reference
			correction := recoveredRunnerCorrection{ReasonCode: cause.ReasonCode, Detail: cause.Detail, Condition: cause.Condition}
			run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
			run.restoreCorrection(&correction)
			advisory, err := f.server.recoveredCompletionCorrectionIsAdvisory(run.SessionID, run, correction)
			if err != nil || advisory != tc.advisory {
				t.Errorf("advisory=%t want=%t err=%v", advisory, tc.advisory, err)
			}
			if advisory {
				run.restoreCorrection(nil) // Same owner as the startup policy branch.
			}
			ctx := context.WithValue(t.Context(), transcriptRunnerChatRunContextKey{}, run)
			_, readErr := f.server.executeAgentWorkspaceFileTool(ctx, f.identity, "read_file", map[string]any{"recovery_condition_id": cause.Condition.ContentID()})
			if tc.advisory && (readErr == nil || run.CorrectionCondition != nil) {
				t.Error("old condition remained readable as current after becoming advisory")
			}
			if !tc.advisory && readErr != nil {
				t.Fatalf("hard condition lost diagnostic read authority: %v", readErr)
			}
			stream, err := f.repo.ValidateLiveRunnerClaim(t.Context(), f.claim)
			if err != nil || stream.InputRevision != f.claim.ClaimedInputRevision {
				t.Fatalf("read rejection came from a stale claim, not retirement: %+v %v", stream, err)
			}
		})
	}
}
