package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/toolgateway"
)

func TestNativeFileReadObservationRequiresActualCurrentAuthority(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt), Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}}
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	base := serverAgentRuntimeToolGateway{server: fixture.server, kernel: fixture.identity, sessionID: run.SessionID, taskRun: run, allowedTools: []string{"read_file"}, suppressHooks: true, fileReadLimitBytes: 4096}
	if err := os.WriteFile(filepath.Join(fixture.projectPath, "observation-proof.txt"), []byte("first line\nsecond line\n"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func(t *testing.T, gateway serverAgentRuntimeToolGateway, callCtx context.Context, input map[string]any, want bool) agentruntime.ToolResult {
		t.Helper()
		arguments, _ := json.Marshal(input)
		result, err := gateway.Execute(callCtx, agentruntime.ToolCall{ID: "native-observation-" + t.Name(), Name: "read_file", Arguments: arguments})
		if result.ReadOnlyObservation != want || want && (err != nil || agentruntime.ClassifyToolResult(result.Value) != agentruntime.ToolResultSucceeded) {
			t.Fatalf("native read witness=%t want=%t err=%v value=%v", result.ReadOnlyObservation, want, err, result.Value)
		}
		return result
	}
	input := func() map[string]any { return map[string]any{"file_path": "observation-proof.txt"} }
	t.Run("actual authorized file", func(t *testing.T) { invoke(t, base, ctx, input(), true) })
	t.Run("normal bounded page", func(t *testing.T) {
		result := invoke(t, base, ctx, map[string]any{"file_path": "observation-proof.txt", "offset": 1, "limit": 1}, true)
		if mapValue(result.Value)["truncated"] != true || numberValue(mapValue(result.Value)["next_offset"]) != 2 {
			t.Fatal("fixture did not exercise a successful bounded window with cursor")
		}
	})
	t.Run("missing source", func(t *testing.T) {
		invoke(t, base, ctx, map[string]any{"file_path": "missing-observation.txt"}, false)
	})
	t.Run("policy rejection", func(t *testing.T) {
		// The initial content sniff sees ordinary text. An explicit line read
		// then reaches invalid UTF8 later in the real stream and must return
		// policy_skip, rather than a valid passive binary/header observation.
		if err := os.WriteFile(filepath.Join(fixture.projectPath, "not-utf8.txt"), []byte(strings.Repeat("a", 513)+"\n\xff\n"), 0600); err != nil {
			t.Fatal(err)
		}
		result := invoke(t, base, ctx, map[string]any{"file_path": "not-utf8.txt", "offset": 1, "limit": 1}, false)
		if mapValue(result.Value)["_policy_skip"] != true {
			t.Fatalf("fixture did not reach real text policy rejection: %v", result.Value)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		invoke(t, base, cancelled, input(), false)
	})
	t.Run("no live transcript actor", func(t *testing.T) {
		gateway := base
		gateway.taskRun = nil
		invoke(t, gateway, context.Background(), input(), false)
	})
	t.Run("retired frame identity", func(t *testing.T) {
		gateway := base
		gateway.kernel = &agentKernelContext{access: fixture.identity.access, workspaceDir: fixture.identity.workspaceDir}
		gateway.kernel.access.Frame.IncarnationID = "retired-incarnation"
		invoke(t, gateway, ctx, input(), false)
	})
	t.Run("foreign principal", func(t *testing.T) {
		bound := *run.Transcript
		bound.Stream.OwnerID = "foreign-principal"
		foreign := &sessionRunnerChatRun{SessionID: run.SessionID, Attempt: run.Attempt, Transcript: &bound}
		gateway := base
		gateway.taskRun = foreign
		invoke(t, gateway, withTranscriptRunnerChatRun(context.Background(), foreign), input(), false)
	})
	t.Run("direct registry execution", func(t *testing.T) {
		gateway := base
		gateway.directExecutor = true
		invoke(t, gateway, ctx, input(), false)
	})
	t.Run("version materialization remains unknown", func(t *testing.T) {
		_, version, err := fixture.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: "immutable-observation", ProjectID: fixture.stream.ProjectID, Name: "immutable.txt", Kind: "text/plain", Content: []byte("immutable source\n")})
		if err != nil {
			t.Fatal(err)
		}
		result := invoke(t, base, ctx, map[string]any{"version_id": version.ID}, false)
		if agentruntime.ClassifyToolResult(result.Value) != agentruntime.ToolResultSucceeded {
			t.Fatalf("version fixture did not complete its real original read: %v", result.Value)
		}
	})
	t.Run("model hint arguments", func(t *testing.T) {
		forged := input()
		forged["read_only"], forged["ReadOnlyObservation"] = true, true
		invoke(t, base, ctx, forged, false)
	})
	t.Run("stale runner claim", func(t *testing.T) {
		interrupted, err := fixture.repo.InterruptRunner(ctx, transcriptstore.InterruptRunnerInput{Claim: fixture.claim, ClientMessageID: "read-observation-handoff", ReasonCode: "runtime_draining", ResumeDetail: "synthetic continuation", AutoResume: true, Destinations: []string{"ws"}})
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := fixture.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, RunnerID: "new-read-observer", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: interrupted.Checkpoint.Sequence})
		if err != nil || !claimed.Claimed || claimed.Claim.ClaimToken == fixture.claim.ClaimToken {
			t.Fatalf("real continuation claim failed: %+v %v", claimed, err)
		}
		invoke(t, base, ctx, input(), false)
	})
}

func TestNativeReadObservationCannotBeProjectedFromMCPResultFields(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	base := serverAgentRuntimeToolGateway{server: fixture.server, kernel: fixture.identity, suppressHooks: true}
	execution := &serverAgentRuntimeGatewayExecution{gateway: base, call: agentruntime.ToolCall{ID: "untrusted-observation", Name: "mcp__unknown__read"}, response: map[string]any{"ok": true, "ReadOnlyObservation": true, "read_only": true, "annotations": map[string]any{"readOnlyHint": true}}}
	invocation := toolgateway.NewInvocation(context.Background(), execution.call.ID, execution.call.Name, nil, execution)
	invocation.CanonicalName = execution.call.Name
	invocation.Input = map[string]any{}
	serverAgentRuntimeGatewayMaterialize(invocation)
	if execution.result.ReadOnlyObservation {
		t.Fatal("untrusted result fields projected a host native-read witness")
	}
}
