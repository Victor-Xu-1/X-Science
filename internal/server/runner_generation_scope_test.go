package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
)

func TestGenerationRecoveryRestoresOutsideReplayAndResetsForChangedProvider(t *testing.T) {
	fixture := newNoProgressReceiptFixture(t)
	cause := &sessionOutputBudgetSaturatedError{cause: errors.New("length"), scope: "provider-a:2048"}
	for i := 0; i < 2; i++ {
		park, err := fixture.server.checkpointGenerationRecovery(context.Background(), fixture.run, cause)
		if err != nil || park {
			t.Fatalf("initial recovery: park=%t err=%v", park, err)
		}
	}
	resumed := &sessionRunnerChatRun{SessionID: fixture.run.SessionID, Transcript: fixture.run.Transcript}
	if _, err := fixture.server.loadTranscriptRunnerReplay(context.Background(), fixture.run.Transcript, 1, 1, resumed); err != nil {
		t.Fatal(err)
	}
	if got := resumed.generationRecoverySnapshot().Consecutive; got != 2 {
		t.Fatalf("restored streak=%d", got)
	}
	if resumed.generationRecoverySnapshot().NotBefore.IsZero() ||
		!resumed.generationRecoverySnapshot().NotBefore.Equal(fixture.run.generationRecoverySnapshot().NotBefore) {
		t.Fatal("durable generation backoff was lost on restoration")
	}
	for i := 3; i <= 4; i++ {
		park, err := fixture.server.checkpointGenerationRecovery(context.Background(), resumed, cause)
		if err != nil || park != (i == 4) {
			t.Fatalf("cycle=%d park=%t err=%v", i, park, err)
		}
	}
	changed := &sessionOutputBudgetSaturatedError{cause: errors.New("length"), scope: "provider-a:4096"}
	if park, err := fixture.server.checkpointGenerationRecovery(context.Background(), resumed, changed); err != nil || park || resumed.generationRecoverySnapshot().Consecutive != 1 {
		t.Fatalf("changed configuration retained an exhausted route: park=%t err=%v", park, err)
	}
	foreign := *fixture.run.Transcript
	foreign.Stream.OwnerID = "another-owner"
	if _, err := fixture.server.loadTranscriptRunnerReplay(context.Background(), fixture.run.Transcript, 1, 1, &sessionRunnerChatRun{Transcript: &foreign}); err == nil {
		t.Fatal("cross-owner recovery was accepted")
	}
}

func TestGenerationRecoveryWaitIsCancellableAndProgressClearsDeadline(t *testing.T) {
	run := &sessionRunnerChatRun{generationRecovery: providerGenerationRecovery{Consecutive: 2, NotBefore: time.Now().Add(time.Hour)}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run.waitGenerationRecovery(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait ignored cancellation: %v", err)
	}
	run.resetGenerationRecoveryProgress()
	if !run.generationRecoverySnapshot().NotBefore.IsZero() {
		t.Fatal("material progress retained an old deadline")
	}
	if err := run.waitGenerationRecovery(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type incompleteProgressModel struct{}

func (incompleteProgressModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	return agentruntime.ModelResponse{}, errors.New("use stream")
}
func (incompleteProgressModel) CompleteStream(_ context.Context, _ agentruntime.ModelRequest, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	for _, event := range []agentruntime.ModelStreamEvent{
		{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: "unfinished candidate"},
		{Kind: agentruntime.ModelStreamEventToolCallBoundary},
	} {
		if err := emit(event); err != nil {
			return agentruntime.ModelResponse{}, err
		}
	}
	return agentruntime.ModelResponse{}, errors.New("truncated tool arguments")
}

func TestResponseContractRetainsInterruptedDraftWithoutPublicBoundary(t *testing.T) {
	client := sessionRunnerResponseContractClient{delegate: incompleteProgressModel{}}
	var events []agentruntime.ModelStreamEvent
	_, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error { events = append(events, event); return nil })
	if err == nil || len(events) != 1 || events[0].Kind != agentruntime.ModelStreamEventContentDelta || events[0].ContentDelta != "unfinished candidate" {
		t.Fatalf("interrupted draft/boundary mismatch: events=%v err=%v", events, err)
	}
}
