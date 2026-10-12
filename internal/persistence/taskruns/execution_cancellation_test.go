package taskruns

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTaskRunCancellationExecutionInstanceSurvivesRestartAndSeparatesReplacement(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	store.now = func() time.Time { return time.UnixMilli(1234567890) }
	input := Input{Action: "start", RunID: "reused", Objective: "controlled task", TaskGraph: explicitTestGraph(), Orchestration: map[string]any{executionInstanceKey: "caller-selected", "cancellationPropagation": map[string]any{"state": "settled"}}}
	first, err := store.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	if ExecutionInstance(first) == "" || ExecutionInstance(first) == "caller-selected" || first.Orchestration["cancellationPropagation"] != nil {
		t.Fatalf("store trusted caller identity/report: %#v", first.Orchestration)
	}
	loaded, found, err := NewStore(root).Get(first.RunID)
	if err != nil || !found || ExecutionInstance(loaded) != ExecutionInstance(first) {
		t.Fatalf("restart identity: found=%t err=%v record=%#v", found, err, loaded)
	}
	replacement, err := store.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.CreatedAt != replacement.CreatedAt || ExecutionInstance(first) == ExecutionInstance(replacement) {
		t.Fatal("same-millisecond replacement did not receive a new opaque identity")
	}
	replacement, _, err = store.Get(replacement.RunID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.CancelForInstance(first.RunID, ExecutionInstance(first), "requested")
	if err == nil || result.JSONCommitted {
		t.Fatalf("stale cancellation committed: result=%#v err=%v", result, err)
	}
	current, _, _ := store.Get(replacement.RunID)
	if !reflect.DeepEqual(current, replacement) {
		t.Fatal("stale cancellation mutated replacement")
	}
	if _, err := store.ReconcileWithRunnerForInstance(first.RunID, ExecutionInstance(first), "completed", "stale evidence"); err == nil {
		t.Fatal("stale reconciliation accepted replacement")
	}
	current, _, _ = store.Get(replacement.RunID)
	if !reflect.DeepEqual(current, replacement) {
		t.Fatal("stale reconciliation mutated replacement")
	}
}

func TestTaskRunCancellationCommitOutcomeSeparatesJSONAndMarkdown(t *testing.T) {
	for _, afterJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_json", true: "after_json"}[afterJSON], func(t *testing.T) {
			store := NewStore(t.TempDir())
			run, err := store.Create(Input{Action: "start", Objective: "controlled write fault", TaskGraph: explicitTestGraph()})
			if err != nil {
				t.Fatal(err)
			}
			if afterJSON {
				if err := os.Remove(run.OutputPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(run.OutputPath, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(store.jsonPath(run.RunID)+".tmp", 0o700); err != nil {
				t.Fatal(err)
			}
			result, err := store.CancelForInstance(run.RunID, ExecutionInstance(run), "requested")
			if err == nil || result.JSONCommitted != afterJSON || (result.ProjectionError != nil) != afterJSON {
				t.Fatalf("wrong commit outcome: result=%#v err=%v", result, err)
			}
			persisted, found, readErr := store.Get(run.RunID)
			if readErr != nil || !found || (persisted.Status == "cancelled") != afterJSON {
				t.Fatalf("durable outcome: found=%t record=%#v err=%v", found, persisted, readErr)
			}
		})
	}
}

func TestTaskRunCancellationLegacyReportAndLateStatesStayTerminal(t *testing.T) {
	store := NewStore(t.TempDir())
	run, err := store.Create(Input{Action: "start", Objective: "legacy task", TaskGraph: explicitTestGraph()})
	if err != nil {
		t.Fatal(err)
	}
	delete(run.Orchestration, executionInstanceKey)
	if _, err := store.Write(run); err != nil {
		t.Fatal(err)
	}
	result, err := store.CancelForInstance(run.RunID, "", "unconfirmed")
	if err != nil || !result.JSONCommitted || stringFromMap(result.Record.Orchestration["cancellationPropagation"].(map[string]any), "state") != "unconfirmed" || !strings.Contains(result.Record.NextActions[0], "Stop generation") {
		t.Fatalf("legacy cancellation overstated stop: result=%#v err=%v", result, err)
	}
	for _, state := range []string{"completed", "failed", "running", "waiting", "blocked", "cancelled"} {
		late, err := store.ReconcileWithRunner(run.RunID, state, "late output")
		if err != nil || !reflect.DeepEqual(late, result.Record) {
			t.Fatalf("late %s changed cancelled graph: err=%v record=%#v", state, err, late)
		}
	}
	if _, err := store.Resume(run.RunID, "retry"); err == nil {
		t.Fatal("cancelled graph resumed")
	}
}
