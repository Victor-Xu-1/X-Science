package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	kernelruntime "x-science/internal/kernel"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestLibraryTaskGatewayPreservesNumericRequestIdentity(t *testing.T) {
	testLibraryNumericAdapters(t, false)
}

func TestLibraryTaskHostPreservesNumericRequestIdentity(t *testing.T) {
	testLibraryNumericAdapters(t, true)
}

func testLibraryNumericAdapters(t *testing.T, hostFirst bool) {
	t.Helper()
	for _, tc := range []struct{ name, original, changed string }{
		{"integer", "9007199254740993", "9007199254740992"},
		{"integer_collision", "9007199254740992", "9007199254740993"},
		{"uint64", "18446744073709551615", "18446744073709551614"},
		{"decimal", "0.123456789012345678901", "0.123456789012345678902"},
		{"large_exponent", "1e400", "2e400"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const callID = "library-numeric-first"
			raw := func(number string) json.RawMessage {
				return json.RawMessage(fmt.Sprintf(`{"operation_id":"a8b8e419-1a65-4ca1-afaa-ea71a55a6372","references":[{"provider":"task","external_id":"numeric-source","title":"Synthetic numeric metadata","metadata":{"nested":[{"value":%s}]}}]}`, number))
			}
			original := raw(tc.original)
			f, ctx, gateway := newLibraryNumericGateway(t, callID, original)
			native := func(id string, arguments json.RawMessage) (any, error) {
				result, err := gateway.Execute(ctx, agentruntime.ToolCall{ID: id, Name: librarySaveToInboxToolName, Arguments: arguments})
				return result.Value, err
			}
			bound := kernelHostExecutionIdentity{access: f.identity.access, transcriptExecution: kernelTranscriptExecutionBinding{
				stream: f.stream, claim: f.claim, outerToolUseID: callID, kernelID: "numeric-kernel", kernelGeneration: 1,
				currentKernelGeneration: func() uint64 { return 1 },
			}}
			host := func(id string, arguments json.RawMessage) (any, error) {
				// The real Host transport decodes Args with UseNumber before the
				// shared handler applies its bounded argument normalization.
				var request map[string]any
				decoder := json.NewDecoder(bytes.NewReader(arguments))
				decoder.UseNumber()
				if err := decoder.Decode(&request); err != nil {
					t.Fatal(err)
				}
				return f.server.handleKernelHostCall(bound, nil)(ctx, kernelruntime.HostCall{
					ID: id, CellID: callID, Method: "host.library.save_to_inbox", Args: []any{request},
				})
			}
			firstAdapter, secondAdapter := native, host
			if hostFirst {
				firstAdapter, secondAdapter = host, native
			}
			first, err := firstAdapter(callID, original)
			if err != nil {
				t.Fatalf("first actual adapter: %v", err)
			}
			if _, ok := first.(workspace.LiteratureTaskInboxReceipt); !ok {
				t.Fatalf("first actual adapter did not accept: %#v", first)
			}
			page, err := f.store.ListLiteratureCandidates(ctx, f.identity.access.UserID, "pending", "")
			if err != nil || len(page.Candidates) != 1 {
				t.Fatalf("readback=%#v err=%v", page, err)
			}
			metadata, err := json.Marshal(page.Candidates[0].Metadata)
			if err != nil || !strings.Contains(string(metadata), `"value":`+tc.original) {
				t.Errorf("stored numeric metadata changed: %s, want %s, err=%v", metadata, tc.original, err)
			}
			replay, err := secondAdapter("library-numeric-replay", original)
			if err != nil || !reflect.DeepEqual(replay, first) {
				t.Errorf("same raw request across native/Host changed receipt: replay=%#v first=%#v err=%v", replay, first, err)
			}
			changed, err := firstAdapter("library-numeric-changed", raw(tc.changed))
			if err == nil && agentruntime.ClassifyToolResult(changed) != agentruntime.ToolResultFailed {
				t.Errorf("changed numeric args borrowed original operation receipt: %#v", changed)
			} else {
				var hostErr *kernelruntime.HostCallError
				if errors.As(err, &hostErr) {
					if hostErr.Code != "request_conflict" {
						t.Errorf("conflict code=%q", hostErr.Code)
					}
				} else if stringValue(mapValue(changed)["code"]) != "request_conflict" {
					t.Errorf("changed numeric args were not an exact-request conflict: %#v %v", changed, err)
				}
			}
			call := agentruntime.ToolCall{ID: callID, Name: librarySaveToInboxToolName, Arguments: original}
			if !gateway.AdmitsToolCall(call) || gateway.ToolCallAdmissionDiagnostic(call) != "" {
				t.Error("original numeric metadata is rejected before execution")
			}
			identity, ok := gateway.ToolCallExecutionIdentity(call)
			if !ok || !strings.Contains(string(identity.Arguments), `"value":`+tc.original) {
				t.Errorf("execution identity changed numeric arguments: %s", identity.Arguments)
			}
		})
	}
}

func newLibraryNumericGateway(t *testing.T, callID string, request json.RawMessage) (*agentSaveArtifactsFixture, context.Context, serverAgentRuntimeToolGateway) {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	payload, err := json.Marshal(map[string]any{"status": "running", "modelToolCalls": []any{map[string]any{"id": callID, "name": librarySaveToInboxToolName, "type": "function", "arguments": request}}})
	if err != nil {
		t.Fatal(err)
	}
	var batch workspace.ToolCallBatch
	var items []workspace.ToolCallBatchItem
	_, _, _, err = f.repo.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{Claim: f.claim, ClientMessageID: "numeric-model-proposal", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: payload,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			var err error
			batch, items, _, err = f.store.CreateToolCallBatchForCheckpointTx(ctx, tx, event)
			return transcriptstore.RunnerCheckpointCommitReceipt{ToolBatch: &transcriptstore.RunnerCheckpointToolBatchReceipt{BatchID: batch.BatchID, CallCount: batch.CallCount}}, err
		}})
	if err != nil || len(items) != 1 {
		t.Fatalf("batch=%#v %v", batch, err)
	}
	batch, err = f.store.ClaimToolCallBatch(t.Context(), workspace.ClaimToolCallBatchInput{Claim: f.claim, BatchID: batch.BatchID, ExpectedStateVersion: batch.StateVersion})
	if err != nil {
		t.Fatal(err)
	}
	started, err := json.Marshal(map[string]any{"status": "running", "toolCallId": callID, "toolName": librarySaveToInboxToolName, "toolPhase": "start", "toolInput": request})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = f.repo.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{Claim: f.claim, ClientMessageID: "numeric-tool-start", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: started,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			_, _, err := f.store.StartToolCallBatchItemTx(ctx, tx, workspace.StartToolCallBatchItemInput{Claim: f.claim, BatchID: batch.BatchID, Ordinal: items[0].Ordinal,
				ExpectedBatchStateVersion: batch.StateVersion, ExpectedItemStateVersion: items[0].StateVersion, StartedEvent: event})
			return transcriptstore.RunnerCheckpointCommitReceipt{}, err
		}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := withTranscriptRunnerChatRun(t.Context(), &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}})
	schemas := []agentruntime.ToolSchema{librarySaveToInboxSchema()}
	gateway := serverAgentRuntimeToolGateway{server: f.server, libraryAccess: f.server.captureLibraryTaskAccess(ctx, f.stream.FrameID), sessionID: f.stream.FrameID,
		allowedTools: []string{librarySaveToInboxToolName}, toolSchemas: schemas, toolValidators: agentRuntimeToolValidators(schemas), hasToolSnapshot: true}
	return f, ctx, gateway
}
