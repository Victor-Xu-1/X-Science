package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
)

func scientificAcquisitionParent(t *testing.T, fixture *agentSaveArtifactsFixture, gateway serverAgentRuntimeToolGateway, call agentruntime.ToolCall, ctx context.Context) (context.Context, transcriptArtifactRun, map[string]any, agentPublicScientificFileRequest) {
	t.Helper()
	ctx = withNativeScientificAcquisitionGateway(fixture.server.withTranscriptArtifactToolSource(ctx, call.ID), gateway)
	run, ok := transcriptArtifactRunFromContext(ctx)
	if !ok {
		t.Fatal("missing real started parent")
	}
	var input map[string]any
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		t.Fatal(err)
	}
	request, err := parseAgentPublicScientificFileRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, run, input, request
}

func scientificAcquisitionEvents(t *testing.T, fixture *agentSaveArtifactsFixture) (transcriptstore.ProjectionSnapshot, []transcriptstore.ProjectedEvent) {
	t.Helper()
	snapshot, err := fixture.repo.GetProjectionSnapshot(t.Context(), fixture.stream.UID, fixture.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := fixture.repo.ListProjectedCoordinateEvents(t.Context(), transcriptstore.ListProjectedEventsInput{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, BranchID: snapshot.BranchID,
		BranchGeneration: snapshot.BranchGeneration, ThroughPublicationSequence: snapshot.ThroughPublicationSequence, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot, events
}

func TestNativeScientificAcquisitionRecovery(t *testing.T) {
	for _, completed := range []bool{false, true} {
		name := "started_only_retries_safe_read"
		if completed {
			name = "completed_source_survives_runner_restart"
		}
		t.Run(name, func(t *testing.T) {
			fixture, gateway, call, ctx, requests, _ := scientificAcquisitionFixture(t, true)
			parentCtx, parent, input, request := scientificAcquisitionParent(t, fixture, gateway, call, ctx)
			if completed {
				if err := fixture.server.acquireNativeScientificSource(parentCtx, parent, call.ID, input, request); err != nil {
					t.Fatal(err)
				}
				if requests.Load() != 1 {
					t.Fatalf("source preparation requests=%d", requests.Load())
				}
			} else {
				phase, err := fixture.server.newNativeScientificAcquisition(parentCtx, gateway.taskRun, parent, call.ID, input, request)
				if err != nil {
					t.Fatal(err)
				}
				if err := fixture.server.checkpointNativeScientificAcquisition(parentCtx, parent, phase, "source_acquisition_start"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(filepath.Join(fixture.projectPath, "records.csv")); !os.IsNotExist(err) {
				t.Fatalf("source stage published a file: %v", err)
			}
			if _, err := fixture.repo.InterruptRunner(t.Context(), transcriptstore.InterruptRunnerInput{
				Claim: fixture.claim, ClientMessageID: "source-restart", ReasonCode: "runtime_draining", AutoResume: true,
			}); err != nil {
				t.Fatal(err)
			}
			reclaimed, err := fixture.repo.ClaimNextRunner(t.Context(), transcriptstore.ClaimNextRunnerInput{RunnerID: "source-recovered", TTL: time.Minute})
			if err != nil || !reclaimed.Claimed || reclaimed.Claim.ClaimToken == fixture.claim.ClaimToken || reclaimed.Claim.RunnerID == fixture.claim.RunnerID {
				t.Fatalf("runner reclaim=%#v err=%v", reclaimed, err)
			}
			// A restarted runner has fresh locks and no prior in-memory tool maps.
			// Recover the parent start from its actual persisted batch instead.
			restored := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID,
				Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: reclaimed.Claim}}
			if err := fixture.server.bindRunnableToolCallBatches(t.Context(), restored); err != nil {
				t.Fatal(err)
			}
			gateway.taskRun = restored
			ctx = withTranscriptRunnerChatRun(context.Background(), restored)
			result, err := gateway.Execute(ctx, call)
			if err != nil || mapValue(result.Value)["ok"] != true || requests.Load() != 2 {
				t.Fatalf("recovery repeated completed acquisition or failed transfer: %#v err=%v requests=%d", result.Value, err, requests.Load())
			}
			_, events := scientificAcquisitionEvents(t, fixture)
			completedCount, modelProposals := 0, 0
			for _, event := range events {
				var payload map[string]any
				_ = json.Unmarshal(event.Event.PayloadJSON, &payload)
				if payload["schema"] == agentPublicScientificAcquisitionSchema && payload["toolPhase"] == "source_acquisition_completed" {
					completedCount++
					if payload["kernelID"] != nil || payload["modelToolCalls"] != nil {
						t.Fatalf("private acquisition fabricated model/kernel authority: %#v", payload)
					}
				}
				if payload["modelToolCalls"] != nil {
					modelProposals++
				}
			}
			if completedCount != 1 || modelProposals != 1 {
				t.Fatalf("durable source completions=%d model proposals=%d", completedCount, modelProposals)
			}
		})
	}
}

func TestNativeScientificDownloadCommittedReplaySurvivesLaterFetchFailure(t *testing.T) {
	for _, source := range []string{"native", "ordinary"} {
		t.Run(source, func(t *testing.T) { testScientificCommittedReplayAfterFailure(t, source) })
	}
}

func testScientificCommittedReplayAfterFailure(t *testing.T, source string) {
	var fetchMu sync.Mutex
	var cancelFetch context.CancelFunc
	fixture, gateway, call, ctx, requests, _ := scientificAcquisitionFixture(t, true, func(*http.Request) {
		fetchMu.Lock()
		cancel := cancelFetch
		fetchMu.Unlock()
		if cancel != nil {
			cancel()
		}
	})
	if source == "ordinary" {
		appendScientificRealFetchedSource(t, fixture, gateway, ctx)
	}
	first, err := gateway.Execute(ctx, call)
	if err != nil || mapValue(first.Value)["ok"] != true || requests.Load() != 2 {
		t.Fatalf("initial real source/download did not commit: result=%#v err=%v requests=%d", first.Value, err, requests.Load())
	}
	firstArtifacts := agentSaveArtifactResults(t, mapValue(first.Value))
	firstSource := stringValue(mapValue(mapValue(first.Value)["download"])["source_tool_call_id"])
	if len(firstArtifacts) != 1 || firstSource == "" {
		t.Fatal("missing original committed version or source identity")
	}
	parentCtx, parent, input, _ := scientificAcquisitionParent(t, fixture, gateway, call, ctx)
	fetchCtx, cancel := context.WithCancel(ctx)
	fetchMu.Lock()
	cancelFetch = cancel
	fetchMu.Unlock()
	fetchInput := map[string]any{"url": input["url"], "limit": nativeScientificSourceLimit}
	fetchCall := agentruntime.ToolCall{ID: "later-independent-fetch", Name: "web_fetch", Arguments: mustMarshalRawMessage(fetchInput)}
	failed, fetchErr := gateway.Execute(fetchCtx, fetchCall)
	cancel()
	fetchMu.Lock()
	cancelFetch = nil
	fetchMu.Unlock()
	if requests.Load() != 3 || fetchErr == nil && agentPublicScientificResultAuthorizesDownload(failed.Value, stringValue(input["url"])) {
		t.Fatalf("controlled canonical fetch did not actually fail: result=%#v err=%v requests=%d", failed.Value, fetchErr, requests.Load())
	}
	if _, _, _, err := fixture.repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: fixture.claim, ClientMessageID: "later-independent-fetch-failed", Phase: transcriptstore.RunnerPhaseExecuting,
		PayloadJSON: mustMarshalRawMessage(map[string]any{"toolName": fetchCall.Name, "toolCallId": fetchCall.ID,
			"toolPhase": "failed", "toolInput": fetchInput, "toolResult": failed.Value}),
	}); err != nil {
		t.Fatal(err)
	}
	// No parent completed checkpoint is written: the artifact commit survived a
	// lost response, and its workspace copy must be restored by exact replay.
	if err := os.Remove(filepath.Join(fixture.projectPath, "records.csv")); err != nil {
		t.Fatal(err)
	}
	t.Run("same_parent_original_version_and_source", func(t *testing.T) {
		replayed, err := gateway.Execute(ctx, call)
		if err != nil || mapValue(replayed.Value)["ok"] != true {
			t.Fatalf("later failure rejected the exact committed parent before replay: result=%#v err=%v requests=%d", replayed.Value, err, requests.Load())
		}
		artifacts := agentSaveArtifactResults(t, mapValue(replayed.Value))
		if requests.Load() != 3 || len(artifacts) != 1 ||
			artifacts[0]["version_id"] != firstArtifacts[0]["version_id"] ||
			mapValue(mapValue(replayed.Value)["download"])["source_tool_call_id"] != firstSource {
			t.Fatalf("later failure invalidated the original committed result: result=%#v err=%v requests=%d", replayed.Value, err, requests.Load())
		}
		if _, err := os.Stat(filepath.Join(fixture.projectPath, "records.csv")); err != nil {
			t.Fatalf("exact replay did not restore the committed workspace content: %v", err)
		}
	})
	t.Run("fresh_parent_stays_blocked", func(t *testing.T) {
		beforeRequests := requests.Load()
		freshCtx := agentPublicScientificToolContext(t, fixture, "fresh-after-failure", input)
		result, err := fixture.server.executeAgentPublicScientificFileDownload(freshCtx, fixture.identity, "fresh-after-failure", input)
		if err == nil && mapValue(result)["ok"] == true || requests.Load() != beforeRequests {
			t.Fatalf("new download bypassed the latest source failure: result=%#v err=%v requests=%d", result, err, requests.Load())
		}
	})
	t.Run("changed_url_stays_rejected", func(t *testing.T) {
		beforeRequests := requests.Load()
		changed := copyMapAny(input)
		changed["url"] = strings.Replace(stringValue(input["url"]), "release=one", "release=two", 1)
		result, err := fixture.server.executeAgentPublicScientificFileDownload(parentCtx, fixture.identity, call.ID, changed)
		if err == nil && mapValue(result)["ok"] == true || requests.Load() != beforeRequests {
			t.Fatalf("changed URL borrowed the original artifact commit: result=%#v err=%v requests=%d", result, err, requests.Load())
		}
	})
	for _, field := range []string{"expected_sha256", "source_tool_call_id"} {
		t.Run("changed_"+field+"_stays_rejected", func(t *testing.T) {
			beforeRequests := requests.Load()
			changed := copyMapAny(input)
			changed[field] = "foreign-source"
			if field == "expected_sha256" {
				changed[field] = strings.Repeat("0", 64)
			}
			result, err := fixture.server.executeAgentPublicScientificFileDownload(parentCtx, fixture.identity, call.ID, changed)
			if err == nil && mapValue(result)["ok"] == true || requests.Load() != beforeRequests {
				t.Fatalf("changed %s borrowed the original selection: result=%#v err=%v requests=%d", field, result, err, requests.Load())
			}
		})
	}
	t.Run("parent_not_in_snapshot_stays_rejected", func(t *testing.T) {
		beforeRequests := requests.Load()
		blocked := gateway
		blocked.allowedTools = []string{"web_fetch"}
		blocked.toolSchemas = nil
		blocked.toolValidators = nil
		result, err := blocked.Execute(ctx, call)
		if err == nil && mapValue(result.Value)["ok"] == true || requests.Load() != beforeRequests {
			t.Fatalf("committed selection bypassed parent snapshot denial: result=%#v err=%v requests=%d", result.Value, err, requests.Load())
		}
	})
	t.Run("foreign_owner_stays_rejected", func(t *testing.T) {
		beforeRequests := requests.Load()
		foreign := *fixture.identity
		foreign.access.UserID = "foreign-owner"
		result, err := fixture.server.executeAgentPublicScientificFileDownload(parentCtx, &foreign, call.ID, input)
		if err == nil && mapValue(result)["ok"] == true || requests.Load() != beforeRequests {
			t.Fatalf("foreign owner borrowed the original artifact commit: result=%#v err=%v requests=%d", result, err, requests.Load())
		}
	})
	t.Run("foreign_claim_stays_rejected", func(t *testing.T) {
		beforeRequests := requests.Load()
		foreign := *parent.Authority
		foreign.Claim.ClaimToken = "foreign-claim"
		foreignCtx := withTranscriptArtifactRun(parentCtx, &foreign, parent.SourceEventID)
		result, err := fixture.server.executeAgentPublicScientificFileDownload(foreignCtx, fixture.identity, call.ID, input)
		if err == nil && mapValue(result)["ok"] == true || requests.Load() != beforeRequests {
			t.Fatalf("foreign claim borrowed the original artifact commit: result=%#v err=%v requests=%d", result, err, requests.Load())
		}
	})
}
