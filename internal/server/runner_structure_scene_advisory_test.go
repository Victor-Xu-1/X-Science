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

	"x-science/internal/agentruntime"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestStructureSceneMissingPreviewCompletesWithVisibleNotice(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	const structure = "ATOM      1  N   ALA A   1       0.000   0.000   0.000  1.00  0.00           N\nEND\n"
	saveRoundPresentationFile(t, f, "base", "base.pdb", structure)
	saveRoundPresentationFile(t, f, "derived", "derived.pdb", structure)
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	model := &artifactReferenceRepairModel{content: "Existing results are available.", testing: t}
	result, err := f.server.runSessionAgentWithArtifactReferenceRepair(context.Background(),
		sessionstore.Session{ID: f.stream.SessionID, Project: &sessionstore.Project{ID: f.stream.ProjectID}},
		agentruntime.Engine{Model: model}, agentruntime.RunRequest{
			Messages: []agentruntime.Message{{Role: "user", Content: "Summarize the supplied results."}},
		}, run, nil)
	if err != nil {
		t.Fatalf("optional preview blocked delivery: %v", err)
	}
	if model.calls != 1 || !strings.Contains(result.FinalMessage.Content, "Structure preview is unverified") {
		t.Fatalf("calls=%d final=%q", model.calls, result.FinalMessage.Content)
	}
	if len(result.Messages) == 0 || result.Messages[len(result.Messages)-1].Content != result.FinalMessage.Content {
		t.Fatal("public final and authoritative assistant message diverged")
	}
}

func TestRecoveredStructureSceneConditionIsAdvisoryButImageIntegrityIsNot(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	scene := newRunnerCorrection(sessionRunnerVisualArtifactValidationReasonCode, "old scene requirement",
		transcriptstore.RunnerCorrectionCondition{Visual: &transcriptstore.RunnerVisualCondition{
			UnboundNames: []string{"missing auxiliary scene"},
		}})
	for _, test := range []struct {
		name      string
		condition *transcriptstore.RunnerCorrectionCondition
		advisory  bool
	}{
		{"scene", scene.Condition, true},
		{"legacy text is not typed evidence", nil, false},
		{"invalid condition", &transcriptstore.RunnerCorrectionCondition{}, false},
		{"empty typed visual", newRunnerCorrection(sessionRunnerVisualArtifactValidationReasonCode, "unknown",
			transcriptstore.RunnerCorrectionCondition{Visual: &transcriptstore.RunnerVisualCondition{}}).Condition, false},
		{"image integrity", newRunnerCorrection(sessionRunnerVisualArtifactValidationReasonCode, "bad bytes",
			transcriptstore.RunnerCorrectionCondition{Visual: &transcriptstore.RunnerVisualCondition{
				Artifacts: []transcriptstore.RunnerVisualConditionArtifact{{
					Name: "image.png", VersionID: "image-version", SHA256: strings.Repeat("a", 64), Failure: "invalid_image_integrity",
				}},
			}}).Condition, false},
		{"unknown image failure", newRunnerCorrection(sessionRunnerVisualArtifactValidationReasonCode, "unknown",
			transcriptstore.RunnerCorrectionCondition{Visual: &transcriptstore.RunnerVisualCondition{
				Artifacts: []transcriptstore.RunnerVisualConditionArtifact{{
					Name: "image.png", VersionID: "image-version", SHA256: strings.Repeat("a", 64), Failure: "unknown",
				}},
			}}).Condition, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := f.server.recoveredCompletionCorrectionIsAdvisory(f.stream.SessionID, nil,
				recoveredRunnerCorrection{ReasonCode: scene.ReasonCode, Detail: scene.Detail, Condition: test.condition})
			if err != nil || got != test.advisory {
				t.Fatalf("advisory=%t want=%t error=%v", got, test.advisory, err)
			}
		})
	}
}

func TestRecoveredStructureSceneDoesNotForceAnotherProviderToolCall(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	claimAnsweredRecoveryFixtureInput(t, f)
	if _, found, err := f.repo.EnsureActiveFrameTaskIntent(context.Background(), f.stream.UID, f.stream.OwnerID); err != nil || !found {
		t.Fatalf("task intent: found=%t error=%v", found, err)
	}
	cause := newRunnerCorrection(sessionRunnerVisualArtifactValidationReasonCode, "old auxiliary scene requirement",
		transcriptstore.RunnerCorrectionCondition{Visual: &transcriptstore.RunnerVisualCondition{
			UnboundNames: []string{"missing auxiliary scene"},
		}})
	interrupted, err := f.repo.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
		Claim: f.claim, ClientMessageID: "old-scene-condition", ReasonCode: cause.ReasonCode,
		ResumeDetail: cause.Detail, Cause: &cause, Resumable: true, AutoResume: true,
		RecoveryContractRevision: sessionRunnerRecoveryContractRevision,
	})
	if err != nil || !interrupted.Created {
		t.Fatalf("persist scene condition: %#v %v", interrupted, err)
	}
	var mu sync.Mutex
	var observed []map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if strings.Contains(string(mustJSON(t, body["messages"])), "Write a brief expert orientation before the task begins.") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "I will inspect the supplied task."},
			}}})
			return
		}
		mu.Lock()
		observed = append(observed, body)
		mu.Unlock()
		// Stop at the actual resumed provider request; this does not pretend
		// that a response was generated or that the scientific task completed.
		http.Error(w, "controlled provider boundary stop", http.StatusBadRequest)
	}))
	defer provider.Close()
	result, runErr := f.server.RunSessionRunnerChatOnce(context.Background(), SessionRunnerChatOptions{
		SessionID: f.stream.SessionID, RunnerID: "scene-policy-resume", Endpoint: provider.URL + "/v1/chat/completions",
		APIKey: "local-test", Model: "local-test", AllowedTools: []string{"read_file", "ask_user"},
		LeaseTTL: time.Minute, MaxAttempts: 1, MaxToolRounds: 1, DisableSkillDiscovery: true, DisableMCPDiscovery: true,
		TranscriptResumeSource: transcriptstore.ResumeSourceCheckpoint, TranscriptCheckpoint: interrupted.Checkpoint.Sequence,
	})
	mu.Lock()
	defer mu.Unlock()
	if len(observed) == 0 {
		t.Fatalf("resumed provider request absent: %#v %v", result, runErr)
	}
	for _, request := range observed {
		if request["tool_choice"] == "required" {
			t.Fatal("obsolete auxiliary scene forced another tool call")
		}
		if strings.Contains(string(mustJSON(t, request["messages"])), sessionRunnerDurableCorrectionContextMarker) {
			t.Fatal("obsolete auxiliary scene remained an active machine-owned obligation")
		}
	}
	if runErr == nil && result.Status == "completed" {
		t.Fatal("controlled provider failure was mislabeled completed")
	}
}
