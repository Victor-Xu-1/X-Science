package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/kernel/detached"
	"x-science/internal/kernelcontract"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/sciencecapability"
	"x-science/internal/skills"
)

func TestKernelAdmissionBudgetUsesSharedContract(t *testing.T) {
	for _, name := range []string{"bash", "python", "r"} {
		t.Run(name, func(t *testing.T) {
			input := map[string]any{"environment": "verified-env", "human_description": "Checking controlled input", "memory_budget_mb": 512}
			schema := agentKernelBashToolSchema()
			if name == "bash" {
				input["command"] = "printf controlled"
			} else {
				input["code"] = "value = 1"
				schema = agentruntime.ToolSchema{Name: name, Parameters: agentKernelLanguageToolParameters(name, "verified-env")}
			}
			gateway := serverAgentRuntimeToolGateway{toolSchemas: []agentruntime.ToolSchema{schema}, toolValidators: agentRuntimeToolValidators([]agentruntime.ToolSchema{schema})}
			if result := gateway.validateAdmittedToolArguments(name, input); result != nil {
				t.Fatalf("advertised budget rejected: %#v", result)
			}
			if _, _, err := kernelcontract.CanonicalInput(name, mustMarshalRawMessage(input)); err != nil {
				t.Fatal(err)
			}
			if err := validateAgentKernelToolInput(name, input); err != nil {
				t.Fatalf("executor disagrees with advertised durable budget: %v", err)
			}
			for _, invalid := range []any{0, -1, 1.5, "512"} {
				bad := copyMapAny(input)
				bad["memory_budget_mb"] = invalid
				if err := validateAgentKernelToolInput(name, bad); err == nil {
					t.Fatalf("invalid budget %v admitted", invalid)
				}
			}
			bad := copyMapAny(input)
			bad["unsupported_execution_option"] = true
			if err := validateAgentKernelToolInput(name, bad); err == nil {
				t.Fatal("unknown executable field was hidden")
			}
		})
	}
	if err := validateAgentKernelToolInput("repl", map[string]any{"code": "value = 1", "memory_budget_mb": 512}); err == nil {
		t.Fatal("compute budget widened the REPL control-plane contract")
	}
}

func TestKernelAdmissionBudgetMinimumReceiptIsNotCapacityRetry(t *testing.T) {
	result, found := kernelResourceAdmissionReceipt(&detached.ExecutorResourceBudgetBelowMinimumError{
		BackendID: "controlled-budget", Generation: 2, RequestedBytes: 512 << 20, MinimumBytes: 768 << 20,
	})
	if !found || result["status"] != "resource_budget_below_minimum" || result["retryable"] != false ||
		result["executed"] != false || result["execution_outcome"] != "not_started" ||
		result["requested_memory_bytes"] != int64(512<<20) || result["minimum_memory_bytes"] != int64(768<<20) {
		t.Fatalf("minimum budget became a retryable capacity wait: %#v", result)
	}
	if !agentruntime.ClassifyToolResult(result).Failed() {
		t.Fatal("unstarted budget rejection became successful computation")
	}
	capacity, found := kernelResourceAdmissionReceipt(&detached.ExecutorResourceUnavailableError{RequiredBytes: 1024 << 20})
	if !found || capacity["status"] != "resource_capacity_unavailable" || capacity["retryable"] != true {
		t.Fatalf("ordinary capacity backstop changed: %#v", capacity)
	}
}

func TestKernelAdmissionNormalizedCheckpointBindsOperation(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	run := &sessionRunnerChatRun{
		SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken,
		Transcript:       &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim},
		ResponseLanguage: "zh-CN", ExecutedSkillNames: []string{"managed-workflow"},
		managedEnvironmentBindings:         map[string]string{"requested-env": "verified-env"},
		managedEnvironmentBindingsHydrated: true,
	}
	script := filepath.Join(f.projectPath, ".x-science", "runtime", "skills", "managed-workflow-abcd", "scripts", "run.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("print('controlled')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := skills.NewCatalog()
	catalog.AddSkill(skills.Skill{Name: "managed-workflow"})
	f.server.skillCatalog = catalog
	f.server.scienceCapabilities = &sciencecapability.Catalog{Capabilities: []sciencecapability.Definition{{
		ID: "managed-capability", AcceptedEngines: []sciencecapability.EngineDefinition{{
			ExecutionPack: sciencecapability.ExecutionPack{
				ID: "managed-capability.controlled", Mode: "local", Skill: "managed-workflow",
				Executable: "python", Script: "executionpacks/run.py",
				Parameters: []sciencecapability.ExecutionParameter{{Name: "report_language", Argument: "--report-language", Type: "string", Evidence: "runtime-response-language"}},
			},
		}},
	}}}
	schemas := []agentruntime.ToolSchema{agentKernelBashToolSchema()}
	gateway := serverAgentRuntimeToolGateway{server: f.server, taskRun: run, kernel: f.identity,
		sessionID: f.stream.SessionID, toolSchemas: schemas, toolValidators: agentRuntimeToolValidators(schemas)}
	original := map[string]any{
		"command":     `cd "` + f.projectPath + `" && python "` + script + `"`,
		"environment": "requested-env", "human_description": "Running controlled analysis", "memory_budget_mb": 1024,
	}
	call := agentruntime.ToolCall{ID: "normalized-kernel-proposal", Name: "bash", Arguments: mustMarshalRawMessage(original)}
	expected := gateway.normalizeAdmittedToolArguments(call.Name, original)
	if expected["environment"] != "verified-env" || strings.HasPrefix(stringValue(expected["command"]), "cd ") ||
		managedExecutionArgumentValues(stringValue(expected["command"]))["--report-language"] != "zh" {
		t.Fatalf("fixture did not exercise actual task normalization: %#v", expected)
	}
	options := SessionRunnerChatOptions{RunnerID: f.claim.RunnerID}
	if err := f.server.checkpointChatModelToolCallsWithContext(ctx, options, run, []agentruntime.ToolCall{call}, gateway); err != nil {
		t.Fatal(err)
	}
	operation, found, err := f.store.GetKernelLocalOperationByToolCall(ctx, f.stream.OwnerID, f.stream.UID, call.ID)
	if err != nil || !found {
		t.Fatalf("operation=%#v found=%t err=%v", operation, found, err)
	}
	expectedJSON := mustMarshalRawMessage(expected)
	digest := sha256.Sum256(expectedJSON)
	if !bytes.Equal(operation.InputJSON, expectedJSON) || operation.InputSHA256 != hex.EncodeToString(digest[:]) || operation.Environment != "verified-env" {
		t.Fatalf("proposal authority differs from actual admission: %#v", operation)
	}
	items, err := f.store.ListToolCallBatchItems(ctx, f.stream.OwnerID, run.ToolBatchIDs[call.ID])
	if err != nil || len(items) != 1 || !bytes.Equal(items[0].ArgumentsJSON, expectedJSON) {
		t.Fatalf("canonical batch decoder did not accept provenance: %#v %v", items, err)
	}
	var payload []byte
	if err := f.db.QueryRow(`SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`, f.stream.UID, operation.SourceEventID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	assertKernelProposalProvenance(t, payload, call.Arguments, expectedJSON)
	if operation.State == workspace.KernelLocalOperationStatePendingApproval {
		operation, err = f.store.ResolveKernelLocalOperationApproval(ctx, workspace.ResolveKernelLocalOperationApprovalInput{
			OwnerUserID: f.stream.OwnerID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
			ApprovalRequestID: operation.ApprovalRequestID, Approved: true, DecisionID: "normalized-proposal-approved",
			Scope: "once", Source: "user", ActorID: f.stream.OwnerID, CurrentClaim: f.claim,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.server.authorizeAgentKernelLocalOperation(withTranscriptRunnerChatRun(ctx, run), f.identity, call, "bash", expected, "verified-env"); err != nil {
		t.Fatalf("unchanged strict execution authorization rejected admitted proposal: %v", err)
	}
	mutated := copyMapAny(expected)
	mutated["command"] = stringValue(expected["command"]) + " --unapproved-extra"
	if _, err := f.server.authorizeAgentKernelLocalOperation(withTranscriptRunnerChatRun(ctx, run), f.identity, call, "bash", mutated, "verified-env"); err == nil {
		t.Fatal("new normalization weakened exact approved-input hash checks")
	}
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 3*time.Second)
	closeErr := f.server.Close(closeCtx)
	cancelClose()
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := workspace.Open(f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	replayRepo, err := reopened.TranscriptRepository(ctx)
	if err != nil {
		t.Fatal(err)
	}
	replay := New(Options{Workspace: reopened, Transcript: replayRepo, FileRoot: t.TempDir()})
	replay.skillCatalog = catalog
	replay.scienceCapabilities = f.server.scienceCapabilities
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := replay.Close(closeCtx); err != nil {
			t.Errorf("close replay server: %v", err)
		}
	})
	stored, found, err := reopened.GetKernelLocalOperation(ctx, f.stream.OwnerID, operation.OperationID)
	if err != nil || !found || !reflect.DeepEqual(stored, operation) {
		t.Fatalf("restart rewrote immutable approval/input: %#v %v", stored, err)
	}
	var replayInput map[string]any
	if err := json.Unmarshal(items[0].ArgumentsJSON, &replayInput); err != nil {
		t.Fatal(err)
	}
	replayGateway := gateway
	replayGateway.server = replay
	if !reflect.DeepEqual(replayGateway.normalizeAdmittedToolArguments("bash", replayInput), replayInput) {
		t.Fatal("canonical dispatch arguments were normalized a second time")
	}
	if _, err := replay.authorizeAgentKernelLocalOperation(withTranscriptRunnerChatRun(ctx, run), f.identity, call, "bash", replayInput, "verified-env"); err != nil {
		t.Fatalf("reopened canonical operation cannot authorize its unchanged dispatch: %v", err)
	}
}

func TestKernelAdmissionLargeProposalProvenanceStaysWithinEventLimit(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	run := &sessionRunnerChatRun{
		SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken,
		Transcript:                         &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim},
		managedEnvironmentBindings:         map[string]string{"requested-env": "verified-env"},
		managedEnvironmentBindingsHydrated: true,
	}
	schemas := []agentruntime.ToolSchema{{Name: "python", Parameters: agentKernelLanguageToolParameters("Python", "verified-env")}}
	gateway := serverAgentRuntimeToolGateway{server: f.server, taskRun: run, kernel: f.identity,
		sessionID: f.stream.SessionID, toolSchemas: schemas, toolValidators: agentRuntimeToolValidators(schemas)}
	// The code is admitted but never executed. Its single canonical copy fits the
	// real event limit; copying the original body as provenance does not.
	code := strings.Repeat("value = 1\n", 60000)
	original := map[string]any{"code": code, "environment": "requested-env", "human_description": "Preparing controlled large source"}
	call := agentruntime.ToolCall{ID: "large-kernel-provenance", Name: "python", Arguments: append(mustMarshalRawMessage(original), '\n')}
	authored := append(json.RawMessage(nil), call.Arguments...)
	if len(authored) <= 512<<10 || len(authored) >= transcriptstore.MaxEventPayloadBytes {
		t.Fatalf("fixture is outside the intended large admitted range: %d bytes", len(authored))
	}
	expected := gateway.normalizeAdmittedToolArguments(call.Name, original)
	expectedJSON, environment, err := kernelcontract.CanonicalInput(call.Name, mustMarshalRawMessage(expected))
	if err != nil || environment != "verified-env" || expected["code"] != code {
		t.Fatalf("fixture did not retain its full source through normalization: environment=%q err=%v", environment, err)
	}
	if err := f.server.checkpointChatModelToolCallsWithContext(ctx, SessionRunnerChatOptions{RunnerID: f.claim.RunnerID}, run, []agentruntime.ToolCall{call}, gateway); err != nil {
		t.Fatalf("large admitted proposal cannot be checkpointed: %v", err)
	}
	if !bytes.Equal(call.Arguments, authored) {
		t.Fatal("checkpoint replaced the original in-memory model proposal")
	}
	operation, found, err := f.store.GetKernelLocalOperationByToolCall(ctx, f.stream.OwnerID, f.stream.UID, call.ID)
	if err != nil || !found {
		t.Fatalf("operation found=%t err=%v", found, err)
	}
	digest := sha256.Sum256(expectedJSON)
	if !bytes.Equal(operation.InputJSON, expectedJSON) || operation.InputSHA256 != hex.EncodeToString(digest[:]) || operation.Environment != environment {
		t.Fatal("large proposal authority was truncated or changed")
	}
	items, err := f.store.ListToolCallBatchItems(ctx, f.stream.OwnerID, run.ToolBatchIDs[call.ID])
	if err != nil || len(items) != 1 || !bytes.Equal(items[0].ArgumentsJSON, expectedJSON) {
		t.Fatalf("large canonical batch differs from the operation: item_count=%d err=%v", len(items), err)
	}
	var payload []byte
	if err := f.db.QueryRow(`SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`, f.stream.UID, operation.SourceEventID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) > transcriptstore.MaxEventPayloadBytes {
		t.Fatalf("checkpoint exceeded the real event limit: %d bytes", len(payload))
	}
	assertKernelProposalProvenance(t, payload, authored, expectedJSON)
	var legacyCopied map[string]any
	if err := json.Unmarshal(payload, &legacyCopied); err != nil {
		t.Fatal(err)
	}
	proposal := legacyCopied["modelToolCalls"].([]any)[0].(map[string]any)
	delete(proposal, "originalArgumentsSHA256")
	delete(proposal, "originalArgumentsByteLength")
	proposal["originalArguments"] = authored
	copied, err := json.Marshal(legacyCopied)
	if err != nil || len(copied) <= transcriptstore.MaxEventPayloadBytes {
		t.Fatalf("fixture did not expose the original body-copy overflow: bytes=%d err=%v", len(copied), err)
	}
	t.Logf("original_bytes=%d checkpoint_bytes=%d old_body_copy_bytes=%d event_limit=%d", len(authored), len(payload), len(copied), transcriptstore.MaxEventPayloadBytes)
}

func assertKernelProposalProvenance(t *testing.T, payload, original, canonical []byte) {
	t.Helper()
	var checkpoint struct {
		ModelToolCalls []struct {
			OriginalArguments           json.RawMessage `json:"originalArguments"`
			OriginalArgumentsSHA256     string          `json:"originalArgumentsSHA256"`
			OriginalArgumentsByteLength int             `json:"originalArgumentsByteLength"`
		} `json:"modelToolCalls"`
	}
	if err := json.Unmarshal(payload, &checkpoint); err != nil || len(checkpoint.ModelToolCalls) != 1 {
		t.Fatal("proposal provenance missing", err)
	}
	digest := sha256.Sum256(original)
	proposal := checkpoint.ModelToolCalls[0]
	if len(proposal.OriginalArguments) != 0 || proposal.OriginalArgumentsSHA256 != hex.EncodeToString(digest[:]) || proposal.OriginalArgumentsByteLength != len(original) {
		t.Fatal("proposal provenance copied a body or disagreed with the original raw bytes")
	}
	// A legacy reader that knows only modelToolCalls/arguments remains compatible;
	// the actual workspace/transcript commit decoders already accepted this event.
	var legacy struct {
		ModelToolCalls []agentruntime.ToolCall `json:"modelToolCalls"`
	}
	if err := json.Unmarshal(payload, &legacy); err != nil || len(legacy.ModelToolCalls) != 1 ||
		!bytes.Equal(legacy.ModelToolCalls[0].Arguments, canonical) {
		t.Fatalf("legacy decoder disagreed with canonical arguments: %v", err)
	}
}

func TestKernelAdmissionNormalizationHonorsCallerCancellationAndTask(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := &Server{}
	run := &sessionRunnerChatRun{}
	if _, err := server.kernelCheckpointAdmissionGateway(ctx, SessionRunnerChatOptions{}, run, "bash", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("fallback ignored parent cancellation: %v", err)
	}
	if _, err := server.kernelCheckpointAdmissionGateway(context.Background(), SessionRunnerChatOptions{}, run, "bash", []serverAgentRuntimeToolGateway{{server: server, taskRun: &sessionRunnerChatRun{}}}); err == nil {
		t.Fatal("foreign task gateway became proposal authority")
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"command":"first","command":"second","environment":"verified-env","human_description":"Checking input"}`),
		json.RawMessage(`{"command":"first"} {"command":"second"}`),
	} {
		if _, err := normalizedKernelCheckpointArguments(serverAgentRuntimeToolGateway{}, "bash", raw); err == nil {
			t.Fatal("normalization erased ambiguous JSON before durable admission")
		}
	}
}
