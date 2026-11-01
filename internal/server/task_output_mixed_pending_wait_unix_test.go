//go:build unix

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

type taskOutputMixedPendingModel struct {
	taskID string
	rounds int
}

func (model *taskOutputMixedPendingModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	if model.rounds == 4 {
		return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "Bounded waits and observations completed; owned work is still pending."}}, nil
	}
	model.rounds++
	wait, _ := json.Marshal(map[string]any{"task_id": model.taskID, "block": true, "timeout": 10})
	return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{
		{ID: fmt.Sprintf("mixed-owned-wait-%d", model.rounds), Name: "TaskOutput", Arguments: wait},
		{ID: fmt.Sprintf("mixed-native-read-%d", model.rounds), Name: "read_file", Arguments: json.RawMessage(`{"file_path":"observation.txt"}`)},
	}}}, nil
}

// Embedding retains the real gateway's admission, canonical identities and
// other interfaces. Only actual completed results are counted for this test.
type taskOutputMixedObservationGateway struct {
	serverAgentRuntimeToolGateway
	positiveWaits int
	reads         int
	unchanged     int
	invalidReads  int
}

func (gateway *taskOutputMixedObservationGateway) Execute(ctx context.Context, call agentruntime.ToolCall) (agentruntime.ToolResult, error) {
	result, err := gateway.serverAgentRuntimeToolGateway.Execute(ctx, call)
	if err == nil && call.Name == "TaskOutput" && result.PendingWorkWait {
		gateway.positiveWaits++
	}
	if call.Name == "read_file" {
		gateway.reads++
		value := mapValue(result.Value)
		if err != nil || agentruntime.ClassifyToolResult(result.Value) != agentruntime.ToolResultSucceeded ||
			agentruntime.ToolResultDidNotExecute(result.Value) || !strings.Contains(stringValue(value["content"]), "owned computation remains pending") {
			gateway.invalidReads++
		}
		if value["reused"] == true && mapValue(value["effect"])["state"] == string(agentruntime.ToolEffectUnchanged) {
			gateway.unchanged++
		}
	}
	return result, err
}

func TestTaskOutputOwnedWaitWithNativeObservationCrossesRepeatBudget(t *testing.T) {
	fixture, run, ctx, taskID, release := newOwnedTaskOutputWaitFixture(t)
	content := []byte("owned computation remains pending\n")
	path := filepath.Join(fixture.projectPath, "observation.txt")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	base := taskOutputWaitGateway(fixture, run)
	base.allowedTools = []string{"TaskOutput", "read_file"}
	base.fileReadLimitBytes = 4096
	// Establish already available evidence with a genuine authorized native
	// read. Subsequent rounds still reread the current file; no reused/effect
	// field or successful response is injected by the fixture.
	first, err := base.Execute(ctx, agentruntime.ToolCall{ID: "initial-native-observation", Name: "read_file", Arguments: json.RawMessage(`{"file_path":"observation.txt"}`)})
	if err != nil || !agentruntime.ToolResultReportsMaterialProgress(first.Value) ||
		!strings.Contains(stringValue(mapValue(first.Value)["content"]), string(bytes.TrimSpace(content))) {
		t.Fatalf("initial real observation failed or lacked fresh evidence: result=%+v err=%v", first.Value, err)
	}
	gateway := &taskOutputMixedObservationGateway{serverAgentRuntimeToolGateway: base}
	model := &taskOutputMixedPendingModel{taskID: taskID}
	result, runErr := (agentruntime.Engine{Model: model, Tools: gateway}).Run(ctx, agentruntime.RunRequest{
		Messages: []agentruntime.Message{{Role: "user", Content: "Wait for the owned process while checking its existing status file."}},
		Tools: []agentruntime.ToolSchema{
			{Name: "TaskOutput", Parameters: map[string]any{"type": "object"}},
			{Name: "read_file", Parameters: map[string]any{"type": "object"}},
		},
		MaxToolRounds: 8, MaxConsecutiveIdenticalToolRounds: 3,
	})
	if runErr != nil || model.rounds != 4 || result.FinalMessage.Content == "" {
		var stopped *agentruntime.ToolRoundNoProgressError
		t.Errorf("legitimate mixed owned wait and native observation interrupted: proposals=%d positive_waits=%d actual_reads=%d no_progress=%t error=%v", model.rounds, gateway.positiveWaits, gateway.reads, errors.As(runErr, &stopped), runErr)
	}
	if gateway.positiveWaits != 4 || gateway.reads != 4 || gateway.unchanged != 4 || gateway.invalidReads != 0 {
		t.Errorf("mixed results were not actual pending waits and unchanged native observations: waits=%d reads=%d unchanged=%d invalid=%d", gateway.positiveWaits, gateway.reads, gateway.unchanged, gateway.invalidReads)
	}
	fixture.server.backgroundShellMu.Lock()
	owned := fixture.server.backgroundShells[taskID] != nil
	fixture.server.backgroundShellMu.Unlock()
	task, found, err := fixture.server.taskStore.Get(taskID)
	if err != nil || !found || task.Status != "running" || !owned {
		t.Fatalf("real held helper was not still owned and pending: found=%t status=%s owned=%t error=%v", found, task.Status, owned, err)
	}
	if actual, err := os.ReadFile(path); err != nil || !bytes.Equal(actual, content) {
		t.Fatalf("read-only observation changed its source: err=%v", err)
	}
	release()
	deadline := time.Now().Add(3 * time.Second)
	for {
		task, found, err = fixture.server.taskStore.Get(taskID)
		if err != nil || !found {
			t.Fatalf("terminal readback failed: found=%t err=%v", found, err)
		}
		if task.Status == "completed" {
			if stringValue(task.Metadata["stdout"]) != "released:done\n" {
				t.Fatalf("original held process lost output: %v", task.Metadata)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("original held process did not settle after IPC release: status=%s", task.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
