package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	transcriptstore "synon-go/internal/persistence/transcript"
)

func TestContinuationShortGenerationsReplanAndRetainHistory(t *testing.T) {
	testContinuationGenerationProgress(t, false, false, false)
}

func TestContinuationShortGenerationsParkOnlyUnchangedRoute(t *testing.T) {
	testContinuationGenerationProgress(t, true, false, false)
}

func TestContinuationLongGenerationsDoNotTriggerFragmentRecovery(t *testing.T) {
	testContinuationGenerationProgress(t, false, true, false)
}

func TestContinuationGrowingBudgetKeepsRecovering(t *testing.T) {
	testContinuationGenerationProgress(t, false, false, true)
}

func testContinuationGenerationProgress(t *testing.T, stall, long, growing bool) {
	t.Helper()
	store, repo, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "progress-project", "progress-frame")
	var mu sync.Mutex
	var requests []string
	rounds := 4
	if growing {
		rounds = 6
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			MaxTokens int `json:"max_tokens"`
			Messages  []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		var text strings.Builder
		for _, message := range request.Messages {
			text.WriteString(message.Content + "\n")
		}
		mu.Lock()
		requests = append(requests, text.String())
		sequence := len(requests)
		mu.Unlock()
		content, finish := fmt.Sprintf("fragment%d", sequence), "length"
		if long {
			content = strings.Repeat(fmt.Sprintf("section%d ", sequence), 80)
		}
		if !stall && sequence == rounds {
			content, finish = " complete response", "stop"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		encoded, _ := json.Marshal(content)
		used := 40
		if growing && request.MaxTokens > 0 {
			used = request.MaxTokens
		}
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%s},\"finish_reason\":%q}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":%d}}\n\ndata: [DONE]\n\n", encoded, finish, used)
	}))
	defer provider.Close()
	root := t.TempDir()
	srv := New(Options{Workspace: store, Transcript: repo, FileRoot: root})
	t.Cleanup(func() { _ = srv.Close(context.Background()) })
	if _, _, err := srv.submitFrameMessage(store, frameMessageSubmission{
		FrameID: "progress-frame", MessageUUID: "progress-message", ClientMessageID: "progress-input", Text: "Write a complete response and preserve earlier work.",
	}); err != nil {
		t.Fatal(err)
	}
	options := SessionRunnerChatOptions{SessionID: "progress-frame", Endpoint: provider.URL, Model: "progress-model", LeaseTTL: time.Minute, RequestTimeout: time.Second, MaxAttempts: 1, DisableSkillDiscovery: true, DisableMCPDiscovery: true}
	var result SessionRunnerCycleResult
	for i := 0; i < rounds; i++ {
		if stall && i == 2 {
			if err := srv.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			srv = New(Options{Workspace: store, Transcript: repo, FileRoot: root})
		}
		options.RunnerID = fmt.Sprintf("progress-runner-%d", i)
		var err error
		result, err = srv.RunSessionRunnerChatOnce(context.Background(), options)
		if err != nil {
			t.Fatal(err)
		}
		if i < rounds-1 && (result.Status != "interrupted" || !result.InterruptionAutoResume) {
			t.Fatalf("premature stop: %#v", result)
		}
		options.SessionID = ""
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != rounds {
		t.Fatalf("provider requests=%d", len(requests))
	}
	changedRoute := strings.Contains(requests[2], "Continuation recovery: next-action")
	if changedRoute == long {
		t.Fatalf("wrong recovery strategy: long=%t changedRoute=%t", long, changedRoute)
	}
	if stall {
		if result.Status != "interrupted" || result.InterruptionAutoResume || !result.AwaitingRecoveryCondition {
			t.Fatalf("unchanged generation route remained hot: %#v", result)
		}
	} else if result.Status != "completed" {
		t.Fatalf("response did not complete: %#v", result)
	}
	stream, found, err := repo.GetFrameStreamBySession(context.Background(), "local", "progress-frame")
	if err != nil || !found {
		t.Fatalf("stream: %t %v", found, err)
	}
	if stall {
		if _, eligible, err := repo.GetAutoResumeCandidate(context.Background(), stream.UID, stream.OwnerID); err != nil || eligible {
			t.Fatalf("parked route kept dispatching: %t %v", eligible, err)
		}
		checkpoint, found, err := repo.LatestResumableCheckpoint(context.Background(), stream.UID, stream.OwnerID)
		if err != nil || !found {
			t.Fatalf("lost recovery point: %t %v", found, err)
		}
		claim, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "manual-recovery", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence})
		if err != nil || !claim.Claimed {
			t.Fatalf("cannot resume parked task: %#v %v", claim, err)
		}
		run := &sessionRunnerChatRun{SessionID: stream.SessionID, Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: claim.Claim}}
		if err := srv.loadProviderContinuation(context.Background(), run); err != nil {
			t.Fatal(err)
		}
		if run.ProviderContinuation == nil || !strings.Contains(run.ProviderContinuation.Content.String(), "fragment1fragment2") {
			t.Fatalf("parked history was not retained: %#v", run.ProviderContinuation)
		}
		return
	}
	events, err := repo.ListProjectedEvents(context.Background(), transcriptstore.ListProjectedEventsInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ThroughPublicationSequence: stream.NextPublication - 1, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var preserved strings.Builder
	for _, event := range events {
		var p map[string]any
		if err := json.Unmarshal(event.ResolvedPayloadJSON, &p); err != nil {
			t.Fatal(err)
		}
		if event.Event.Type == "content_delta" || event.Event.Type == "assistant_message" {
			preserved.WriteString(stringValue(p["text"]))
		}
		if candidate, private, err := privateProviderCandidateText(p); err == nil && private {
			preserved.WriteString(candidate)
		}
	}
	if !long && (!strings.Contains(preserved.String(), "fragment1") || !strings.Contains(preserved.String(), "fragment2")) {
		t.Fatalf("recovery erased earlier accepted bytes: %q", preserved.String())
	}
}
