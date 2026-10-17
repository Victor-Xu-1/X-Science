package server

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestRunnerPreparationFreshCompactionEpisodeSurvivesResume(t *testing.T) {
	const ownerID, projectID, frameID = "local", "episode-project", "episode-frame"
	const threshold = 100
	ctx := t.Context()
	store, repo, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, ownerID, projectID, frameID)
	server := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	t.Cleanup(func() { closeTestServer(t, server) })
	for key, value := range map[string]any{"autoCompactEnabled": true, "autoCompactTokenThreshold": float64(threshold)} {
		if _, err := server.settingsStore.Set(configStoreKey(key), value); err != nil {
			t.Fatalf("setup: configure %s: %v", key, err)
		}
	}
	input := strings.Repeat("abcd", 2000)
	if _, _, err := server.submitFrameMessage(store, frameMessageSubmission{
		FrameID: frameID, MessageUUID: "episode-input", ClientMessageID: "episode-input", Text: input,
	}); err != nil {
		t.Fatalf("setup: submit input: %v", err)
	}
	stream, found, err := repo.GetFrameStreamBySession(ctx, ownerID, frameID)
	if err != nil || !found {
		t.Fatalf("setup: stream found=%t err=%v", found, err)
	}
	claimed, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: ownerID, RunnerID: "episode-initial", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("setup: initial claim=%#v err=%v", claimed, err)
	}
	session, err := server.loadTranscriptFrameSessionProjection(stream)
	if err != nil {
		t.Fatalf("setup: session projection: %v", err)
	}
	options := SessionRunnerChatOptions{SessionID: frameID, RunnerID: claimed.Claim.RunnerID, Model: "test-model", ReplayLimit: 100}
	newRun := func(claim transcriptstore.RunnerClaim) *sessionRunnerChatRun {
		run := &sessionRunnerChatRun{
			SessionID: frameID, Attempt: int(claim.Attempt), ClaimToken: claim.ClaimToken,
			Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: claim},
		}
		if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
			t.Fatalf("setup: initialize preparation: %v", err)
		}
		return run
	}
	loadReplay := func(run *sessionRunnerChatRun) []eventjournal.Entry {
		entries, err := server.loadTranscriptRunnerReplay(ctx, run.Transcript, 100, 100)
		if err != nil {
			t.Fatalf("setup: load durable replay: %v", err)
		}
		run.AfterEventID = maxEntryIDFromEntries(entries)
		return entries
	}
	compactIDs := func(entries []eventjournal.Entry) []int64 {
		var ids []int64
		for _, entry := range entries {
			if isCompactJournalEntry(entry) {
				ids = append(ids, entry.EventID)
			}
		}
		return ids
	}
	run := newRun(claimed.Claim)
	if err := server.checkpointSessionRunnerPreparationStage(ctx, run, "history_compaction"); err != nil {
		t.Fatalf("setup: initial preparation checkpoint: %v", err)
	}
	entries, first, err := server.autoCompactSessionForRunner(ctx, options, session, loadReplay(run), run)
	if err != nil || !first.Triggered {
		t.Fatalf("setup: production compaction=%+v err=%v", first, err)
	}
	boundaries := compactIDs(entries)
	archives, err := store.ListCompactionArchives(frameID)
	if err != nil || len(boundaries) != 1 || len(archives) != 1 {
		t.Fatalf("setup: compact boundaries=%v archives=%d err=%v", boundaries, len(archives), err)
	}
	if err := server.checkpointSessionRunnerPreparationStage(ctx, run, "context_replay"); err != nil {
		t.Fatalf("setup: post-compact preparation checkpoint: %v", err)
	}
	// Graceful drain expires the real lease without adding a model/tool outcome
	// or new input. Checkpoint resume reuses the attempt with a new claim token.
	interrupted, err := repo.InterruptRunner(ctx, transcriptstore.InterruptRunnerInput{
		Claim: claimed.Claim, ClientMessageID: "episode-drain", ReasonCode: "runtime_draining",
		AutoResume: true, Resumable: true,
	})
	if err != nil || !interrupted.Created {
		t.Fatalf("setup: drain created=%t err=%v", interrupted.Created, err)
	}
	resumed, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: ownerID, RunnerID: "episode-resumed", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: interrupted.Checkpoint.Sequence,
	})
	if err != nil || !resumed.Claimed {
		t.Fatalf("setup: checkpoint claim=%#v err=%v", resumed, err)
	}
	if resumed.Claim.Attempt != claimed.Claim.Attempt || resumed.Claim.ClaimToken == claimed.Claim.ClaimToken ||
		resumed.Claim.ResumeSource != transcriptstore.ResumeSourceCheckpoint ||
		resumed.Claim.ResumeCheckpoint != interrupted.Checkpoint.Sequence ||
		resumed.Claim.ClaimedInputRevision != claimed.Claim.ClaimedInputRevision {
		t.Fatal("setup: resume did not preserve the input and attempt under a new checkpoint claim")
	}
	options.RunnerID = resumed.Claim.RunnerID
	run = newRun(resumed.Claim)
	if err := server.checkpointSessionRunnerPreparationStage(ctx, run, "history_compaction"); err != nil {
		t.Fatalf("setup: resumed preparation checkpoint: %v", err)
	}
	entries = loadReplay(run)
	estimated, err := sessionRunnerProviderContextTokenEstimate(options.SystemPrompt, entries)
	if err != nil || estimated <= threshold || !runnerReplayFreshlyCompacted(entries) ||
		runnerContextPressureRequiringCompaction(entries) != "" {
		t.Fatalf("setup: resumed replay tokens=%d fresh=%t pressure=%q err=%v", estimated,
			runnerReplayFreshlyCompacted(entries), runnerContextPressureRequiringCompaction(entries), err)
	}

	_, second, err := server.autoCompactSessionForRunner(ctx, options, session, entries, run)
	if err != nil {
		t.Fatalf("resumed preparation: %v", err)
	}
	entries = loadReplay(run)
	afterArchives, err := store.ListCompactionArchives(frameID)
	if err != nil {
		t.Fatalf("read archives after resumed preparation: %v", err)
	}
	if second.Triggered || !reflect.DeepEqual(compactIDs(entries), boundaries) || !reflect.DeepEqual(afterArchives, archives) {
		t.Fatalf("fresh compaction repeated without semantic progress: triggered=%t boundaries=%v want=%v archives=%d want=%d",
			second.Triggered, compactIDs(entries), boundaries, len(afterArchives), len(archives))
	}
	users := 0
	for _, entry := range entries {
		if stringValue(entry.Message["type"]) == "message" && stringValue(entry.Message["role"]) == "user" {
			users++
			if stringValue(entry.Message["text"]) != input {
				t.Fatal("resumed preparation changed the original input")
			}
		}
	}
	if users != 1 {
		t.Fatalf("resumed preparation user messages=%d, want 1", users)
	}
	messages, err := sessionEntriesToProviderMessages(options.SystemPrompt, entries)
	if err != nil {
		t.Fatalf("build resumed provider request: %v", err)
	}
	delegate := &preResolvedDynamicModelClient{}
	client := &sessionRunnerDynamicModelClient{
		initialReady: true, initial: sessionRunnerResolvedModelClient{client: delegate, model: options.Model},
		contextOptions: options,
		contextBudget:  newSessionRunnerRequestContextBudget(server, options, entries, run, second.episode),
	}
	response, err := client.Complete(ctx, agentruntime.ModelRequest{Messages: agentRuntimeMessagesFromChat(messages)})
	if err != nil || delegate.calls.Load() != 1 || response.Message.Content != "pre-resolved" {
		t.Fatalf("resumed request did not advance: calls=%d response=%q err=%v", delegate.calls.Load(), response.Message.Content, err)
	}
}
