//go:build unix

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	taskstore "x-science/internal/persistence/tasks"
	transcriptstore "x-science/internal/persistence/transcript"

	"golang.org/x/sys/unix"
)

type taskOutputPendingModel struct {
	taskID string
	waits  int
}

func (model *taskOutputPendingModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	if model.waits == 4 {
		return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "Bounded waits completed; the owned process remains pending."}}, nil
	}
	model.waits++
	input, _ := json.Marshal(map[string]any{"task_id": model.taskID, "block": true, "timeout": 10})
	return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{ID: fmt.Sprintf("owned-wait-%d", model.waits), Name: "TaskOutput", Arguments: input}}}}, nil
}

func TestTaskOutputOwnedBoundedWaitCrossesRepeatBudget(t *testing.T) {
	fixture, run, ctx, taskID, release := newOwnedTaskOutputWaitFixture(t)
	model := &taskOutputPendingModel{taskID: taskID}
	gateway := taskOutputWaitGateway(fixture, run)
	result, runErr := (agentruntime.Engine{Model: model, Tools: gateway}).Run(ctx, agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Wait for the current owned process output."}}, Tools: []agentruntime.ToolSchema{{Name: "TaskOutput", Parameters: map[string]any{"type": "object"}}}, MaxToolRounds: 8, MaxConsecutiveIdenticalToolRounds: 3})
	if runErr != nil || model.waits != 4 || result.FinalMessage.Content == "" {
		t.Errorf("legitimate owned bounded waits interrupted: requests=%d final=%q error=%v", model.waits, result.FinalMessage.Content, runErr)
	}
	fixture.server.backgroundShellMu.Lock()
	owned := fixture.server.backgroundShells[taskID] != nil
	fixture.server.backgroundShellMu.Unlock()
	task, found, err := fixture.server.taskStore.Get(taskID)
	if err != nil || !found || task.Status != "running" || !owned {
		t.Fatalf("helper was not still owned/pending: found=%t status=%s registered=%t error=%v", found, task.Status, owned, err)
	}
	release()
	deadline := time.Now().Add(3 * time.Second)
	for {
		task, found, err = fixture.server.taskStore.Get(taskID)
		if err != nil || !found {
			t.Fatalf("terminal readback: found=%t error=%v", found, err)
		}
		if task.Status == "completed" {
			if stringValue(task.Metadata["stdout"]) != "released:done\n" {
				t.Errorf("output lost: %+v", task.Metadata)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("owned helper did not settle: %+v", task)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newOwnedTaskOutputWaitFixture(t *testing.T) (*agentSaveArtifactsFixture, *sessionRunnerChatRun, context.Context, string, func()) {
	t.Helper()
	fixture := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt), Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}}
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	path := filepath.Join(fixture.server.fileRoot, "owned-input.fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	started, err := fixture.server.executeShellTool(ctx, "Bash", map[string]any{"command": "read -r token < owned-input.fifo; printf 'released:%s\\n' \"$token\"", "workdir": ".", "run_in_background": true})
	if err != nil {
		t.Fatal(err)
	}
	taskID := stringValue(mapValue(started)["task_id"])
	var writer *os.File
	deadline := time.Now().Add(3 * time.Second)
	for writer == nil {
		fd, openErr := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK, 0)
		if openErr == nil {
			writer = os.NewFile(uintptr(fd), path)
			break
		}
		if openErr != unix.ENXIO || time.Now().After(deadline) {
			t.Fatalf("owned helper did not reach IPC read: %v", openErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_, _ = writer.WriteString("done\n")
			_ = writer.Close()
		}
	}
	t.Cleanup(release)
	fixture.server.backgroundShellMu.Lock()
	entry := fixture.server.backgroundShells[taskID]
	fixture.server.backgroundShellMu.Unlock()
	if entry == nil || entry.owner == nil {
		t.Fatal("fixture: original launch did not capture canonical owner")
	}
	return fixture, run, ctx, taskID, release
}

func taskOutputWaitGateway(fixture *agentSaveArtifactsFixture, run *sessionRunnerChatRun) serverAgentRuntimeToolGateway {
	return serverAgentRuntimeToolGateway{server: fixture.server, kernel: fixture.identity, sessionID: run.SessionID, taskRun: run, allowedTools: []string{"TaskOutput"}, suppressHooks: true}
}

func TestTaskOutputWaitProofRequiresCurrentOwnedBlockingWork(t *testing.T) {
	for _, name := range []string{"positive", "nonblocking", "missing", "unknown-owner", "terminal", "foreign-principal", "old-incarnation", "other-branch", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			fixture, run, ctx, id, _ := newOwnedTaskOutputWaitFixture(t)
			input := map[string]any{"task_id": id, "block": true, "timeout": 10}
			want := name == "positive"
			switch name {
			case "nonblocking":
				input["block"] = false
			case "missing":
				input["task_id"] = "not-an-owned-task"
			case "unknown-owner", "terminal":
				status := "running"
				if name == "terminal" {
					status = "completed"
				}
				task, err := fixture.server.taskStore.CreateWithOptions(taskstore.CreateOptions{Subject: "unregistered", Status: status, Metadata: map[string]any{"background_owner": map[string]any{"UserID": fixture.stream.OwnerID, "FrameID": fixture.stream.FrameID}}})
				if err != nil {
					t.Fatal(err)
				}
				input["task_id"] = task.ID
			case "foreign-principal":
				copyAuthority := *run.Transcript
				copyAuthority.Stream.OwnerID = "another-account"
				run = &sessionRunnerChatRun{SessionID: run.SessionID, Attempt: run.Attempt, Transcript: &copyAuthority}
				ctx = withTranscriptRunnerChatRun(context.Background(), run)
			case "old-incarnation", "other-branch":
				fixture.server.backgroundShellMu.Lock()
				entry := fixture.server.backgroundShells[id]
				copyOwner := *entry.owner
				if name == "old-incarnation" {
					copyOwner.FrameIncarnationID = "retired-incarnation"
				} else {
					copyOwner.BranchID = "other-owned-branch"
				}
				entry.owner = &copyOwner
				fixture.server.backgroundShellMu.Unlock()
			case "cancelled":
				cancelCtx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelCtx
			}
			arguments, _ := json.Marshal(input)
			result, err := taskOutputWaitGateway(fixture, run).Execute(ctx, agentruntime.ToolCall{ID: "proof-" + name, Name: "TaskOutput", Arguments: arguments})
			if name != "cancelled" && err != nil && !(name == "foreign-principal" && errors.Is(err, transcriptstore.ErrOwnerMismatch)) {
				t.Fatal(err)
			}
			if result.PendingWorkWait != want {
				t.Errorf("typed wait proof=%t want=%t error=%v", result.PendingWorkWait, want, err)
			}
		})
	}
}

func TestTaskOutputWaitOwnerSurvivesLegitimateRunnerContinuation(t *testing.T) {
	fixture, run, ctx, id, _ := newOwnedTaskOutputWaitFixture(t)
	interrupted, err := fixture.repo.InterruptRunner(ctx, transcriptstore.InterruptRunnerInput{Claim: fixture.claim, ClientMessageID: "pending-wait-handoff", ReasonCode: "runtime_draining", ResumeDetail: "test continuation", AutoResume: true, Destinations: []string{"ws"}})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := fixture.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, RunnerID: "continued-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: interrupted.Checkpoint.Sequence})
	if err != nil || !claimed.Claimed || claimed.Claim.ClaimToken == fixture.claim.ClaimToken {
		t.Fatalf("continuation claim=%+v error=%v", claimed, err)
	}
	continued := &sessionRunnerChatRun{SessionID: run.SessionID, Attempt: int(claimed.Claim.Attempt), Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: claimed.Claim}}
	arguments, _ := json.Marshal(map[string]any{"task_id": id, "block": true, "timeout": 10})
	result, err := taskOutputWaitGateway(fixture, continued).Execute(withTranscriptRunnerChatRun(context.Background(), continued), agentruntime.ToolCall{ID: "continued-owned-wait", Name: "TaskOutput", Arguments: arguments})
	if err != nil || !result.PendingWorkWait {
		t.Fatalf("same logical owner rejected across attempts: pending=%t error=%v", result.PendingWorkWait, err)
	}
	stale, err := taskOutputWaitGateway(fixture, run).Execute(ctx, agentruntime.ToolCall{ID: "stale-claim-wait", Name: "TaskOutput", Arguments: arguments})
	if stale.PendingWorkWait {
		t.Fatalf("stale lease gained pending proof: error=%v", err)
	}
	if err != nil && !errors.Is(err, transcriptstore.ErrClaimStale) {
		t.Logf("stale invocation rejected by existing gateway: %v", err)
	}
	// Checkpoint continuation deliberately reuses its logical attempt. Admit a
	// real synthetic user followup after the terminal boundary to authorize a
	// new attempt without changing the background command's frame/branch owner.
	if _, _, _, err := fixture.repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{
		Claim: claimed.Claim, ClientMessageID: "pending-wait-segment-failed", Status: "failed",
		PayloadJSON: []byte(`{"status":"failed","detail":"synthetic segment ended; background command remains owned"}`),
	}); err != nil {
		t.Fatal(err)
	}
	fixture.server.backgroundShellMu.Lock()
	launchOwner := *fixture.server.backgroundShells[id].owner
	fixture.server.backgroundShellMu.Unlock()
	if event, repeated, err := fixture.server.submitFrameMessage(fixture.store, frameMessageSubmission{
		FrameID: fixture.stream.FrameID, MessageUUID: "pending-wait-user-followup", ClientMessageID: "pending-wait-user-followup",
		Text: "Continue waiting for the same registered background command.",
	}); err != nil || repeated || event.FrameID != fixture.stream.FrameID {
		t.Fatalf("synthetic user followup: event=%+v repeated=%t error=%v", event, repeated, err)
	}
	freshStream, found, err := fixture.repo.GetFrameStreamBySession(context.Background(), fixture.stream.OwnerID, fixture.stream.SessionID)
	if err != nil || !found {
		t.Fatalf("continued stream found=%t error=%v", found, err)
	}
	retried, err := fixture.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: freshStream.UID, OwnerID: freshStream.OwnerID, RunnerID: "followup-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !retried.Claimed || retried.Claim.Attempt <= fixture.claim.Attempt || retried.Claim.ClaimedInputRevision <= fixture.claim.ClaimedInputRevision {
		t.Fatalf("new attempt claim=%+v error=%v", retried, err)
	}
	retryRun := &sessionRunnerChatRun{SessionID: run.SessionID, Attempt: int(retried.Claim.Attempt), Transcript: &transcriptRunnerAuthority{Stream: freshStream, Claim: retried.Claim}}
	retryContext := withTranscriptRunnerChatRun(context.Background(), retryRun)
	if currentOwner := fixture.server.currentBackgroundShellOwner(retryContext); currentOwner == nil || *currentOwner != launchOwner {
		t.Fatalf("user followup changed logical owner: launch=%+v current=%+v", launchOwner, currentOwner)
	}
	result, err = taskOutputWaitGateway(fixture, retryRun).Execute(retryContext, agentruntime.ToolCall{ID: "retried-owned-wait", Name: "TaskOutput", Arguments: arguments})
	if err != nil || !result.PendingWorkWait {
		t.Fatalf("same owned command rejected on new attempt: pending=%t error=%v", result.PendingWorkWait, err)
	}
}
