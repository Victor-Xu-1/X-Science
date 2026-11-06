package server

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestHostGrantSnapshotFingerprintPreservesOriginalSlice(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	grants := []hostGrant{{ID: "b", Path: "/B", Mode: "read", CreatedAt: now}, {ID: "a", Path: "/a", Mode: "read_write", CreatedAt: now}}
	before := append([]hostGrant(nil), grants...)
	first, err := hostGrantSnapshotResult("owner", grants)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hostGrantSnapshotResult("owner", []hostGrant{grants[1], grants[0]})
	if err != nil || !reflect.DeepEqual(first, second) || !reflect.DeepEqual(grants, before) {
		t.Fatal("fingerprint reordered/mutated original grants or depended on row order")
	}
	other, _ := hostGrantSnapshotResult("other", grants)
	grants[0].Path = "/b"
	changed, _ := hostGrantSnapshotResult("owner", grants)
	if first["snapshotRevision"] == other["snapshotRevision"] || first["snapshotRevision"] == changed["snapshotRevision"] {
		t.Fatal("fingerprint merged owners or changed path bytes")
	}
	empty, _ := hostGrantSnapshotResult("owner", nil)
	emptySlice, _ := hostGrantSnapshotResult("owner", []hostGrant{})
	if !reflect.DeepEqual(empty, emptySlice) || !validDirectoryFingerprint(stringValue(first["snapshotRevision"])) {
		t.Fatal("empty-snapshot or content fingerprint contract is inconsistent")
	}
}

type directoryCaptureModel struct{ requests []agentruntime.ModelRequest }

func (model *directoryCaptureModel) Complete(_ context.Context, request agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	model.requests = append(model.requests, agentruntime.SnapshotModelRequest(request))
	return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "Done."}}, nil
}

func (model *directoryCaptureModel) CompleteStream(ctx context.Context, request agentruntime.ModelRequest, _ func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	return model.Complete(ctx, request)
}

type directoryNonStreamingModel struct{ capture *directoryCaptureModel }

func (model directoryNonStreamingModel) Complete(ctx context.Context, request agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	return model.capture.Complete(ctx, request)
}

func directoryModel(projection *runnerDirectoryContext, delegate agentruntime.ModelClient) *sessionRunnerDynamicModelClient {
	return &sessionRunnerDynamicModelClient{directoryContext: projection, initialReady: true,
		initial: sessionRunnerResolvedModelClient{client: delegate, model: "directory-model", identity: "directory-provider"}}
}

func directoryProjectionFixture(t *testing.T) (*agentSaveArtifactsFixture, *sessionRunnerChatRun, agentruntime.ModelRequest) {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	const callID = "directory-original"
	raw, _ := seedDirectoryFreshnessDiscovery(t, f, callID)
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	request := agentruntime.ModelRequest{Tools: agentPermissionToolSchemas()[:1], ToolChoice: "auto",
		Metadata: map[string]any{"original": true}, Headers: map[string]string{"X-Original": "kept"},
		Messages: []agentruntime.Message{{Role: "user", Content: "Use the existing directory discovery."},
			{Role: "tool", ToolCallID: callID, Content: string(raw)}}}
	return f, run, request
}

func directoryNoticeCount(request agentruntime.ModelRequest, notice string) int {
	count := 0
	for _, message := range request.Messages {
		count += strings.Count(message.Content, notice)
	}
	return count
}

func TestRunnerDirectoryContextUsesOneActualCallLocalRequest(t *testing.T) {
	f, run, request := directoryProjectionFixture(t)
	projection := newRunnerDirectoryContext(f.server, run)
	if prepared, err := projection.prepare(t.Context(), request); err != nil || !reflect.DeepEqual(prepared, request) {
		t.Fatalf("complete current discovery changed a request: %v", err)
	}
	if _, err := f.server.upsertHostGrant(f.stream.OwnerID, t.TempDir(), "read"); err != nil {
		t.Fatal(err)
	}
	original := agentruntime.SnapshotModelRequest(request)
	for _, mode := range []string{"complete", "stream", "stream_fallback"} {
		t.Run(mode, func(t *testing.T) {
			capture := &directoryCaptureModel{}
			var delegate agentruntime.ModelClient = capture
			if mode == "stream_fallback" {
				delegate = directoryNonStreamingModel{capture}
			}
			client := directoryModel(projection, delegate)
			memory := &memoryExtractionCallIdentity{}
			ctx := context.WithValue(t.Context(), memoryExtractionCallIdentityKey{}, memory)
			var err error
			if mode == "complete" {
				_, err = client.Complete(ctx, request)
			} else {
				_, err = client.CompleteStream(ctx, request, nil)
			}
			if err != nil || len(capture.requests) != 1 || directoryNoticeCount(capture.requests[0], directoryChangedNotice) != 1 {
				t.Fatalf("actual %s delegate lacks exactly one notice: %v", mode, err)
			}
			actual, identity := memory.read()
			if actual == nil || !reflect.DeepEqual(*actual, capture.requests[0]) || identity.Model != "directory-model" ||
				!reflect.DeepEqual(request, original) || capture.requests[0].ToolChoice != request.ToolChoice {
				t.Fatal("Memory/provider input diverged, tool choice changed, or projection accumulated in source history")
			}
		})
	}
}

func TestRunnerDirectoryContextDoesNotInduceUnrelatedOrControlDiscovery(t *testing.T) {
	t.Run("never_observed", func(t *testing.T) {
		f := newAgentSaveArtifactsFixture(t)
		run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
		request := agentruntime.ModelRequest{Tools: agentPermissionToolSchemas()[:1], Messages: []agentruntime.Message{{Role: "user", Content: "Continue the task."}}}
		saved := f.server.settingsStore
		f.server.settingsStore = nil
		defer func() { f.server.settingsStore = saved }()
		prepared, err := newRunnerDirectoryContext(f.server, run).prepare(t.Context(), request)
		if err != nil || !reflect.DeepEqual(prepared, request) {
			t.Fatalf("never-observed discovery induced a directory instruction: %v", err)
		}
	})
	t.Run("hidden_control", func(t *testing.T) {
		f := newAgentSaveArtifactsFixture(t)
		loadSkillControlCatalog(t, f)
		finishSkillControlInput(t, f)
		appendSkillControlInput(t, f, "directory-skill-control", "HIDDEN_CONTROL", true)
		run := claimSkillControlInput(t, f)
		_, control, err := f.server.prepareConversationSkillContinuation(t.Context(), SessionRunnerChatOptions{}, run)
		if err != nil || control == nil {
			t.Fatalf("original control preparation failed: %v", err)
		}
		run.currentControl = control
		if newRunnerDirectoryContext(f.server, run) != nil {
			t.Fatal("legitimate hidden control acquired ordinary directory projection")
		}
	})
	t.Run("auxiliary", func(t *testing.T) {
		f, run, request := directoryProjectionFixture(t)
		prepared, err := newRunnerDirectoryContext(f.server, run).prepare(context.WithValue(t.Context(), auxiliaryContextUsageKey{}, true), request)
		if err != nil || !reflect.DeepEqual(prepared, request) {
			t.Fatalf("auxiliary presentation request was changed: %v", err)
		}
	})
}

func TestRunnerDirectoryContextKeepsScopeAndOptionalFailureBoundaries(t *testing.T) {
	for _, kind := range []string{"foreign_owner", "late_original_claim", "unavailable_grants", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			f, run, request := directoryProjectionFixture(t)
			projection := newRunnerDirectoryContext(f.server, run)
			ctx := t.Context()
			var expected error
			switch kind {
			case "foreign_owner":
				projection.claim.OwnerID = "another-owner"
				expected = transcriptstore.ErrOwnerMismatch
			case "late_original_claim":
				finishSkillControlInput(t, f)
				appendSkillControlInput(t, f, "directory-new-input", "A new ordinary task.", false)
				next := claimSkillControlInput(t, f)
				run.Transcript.Claim = next.Transcript.Claim
				expected = transcriptstore.ErrClaimStale
			case "unavailable_grants":
				saved := f.server.settingsStore
				f.server.settingsStore = nil
				defer func() { f.server.settingsStore = saved }()
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
				expected = context.Canceled
			}
			capture := &directoryCaptureModel{}
			_, err := directoryModel(projection, capture).Complete(ctx, request)
			if expected != nil {
				if !errors.Is(err, expected) || len(capture.requests) != 0 {
					t.Fatalf("stale/cancelled owner reached provider: error=%v", err)
				}
			} else if err != nil || len(capture.requests) != 1 || directoryNoticeCount(capture.requests[0], directoryUnknownNotice) != 1 {
				t.Fatalf("optional grant unavailability blocked or pretended to verify a request: %v", err)
			}
		})
	}
}

func beginDirectoryContextTool(t *testing.T, f *agentSaveArtifactsFixture, callID string) (context.Context, *sessionRunnerChatRun, agentruntime.ToolCall) {
	t.Helper()
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	ctx := withTranscriptRunnerChatRun(t.Context(), run)
	call := agentruntime.ToolCall{ID: callID, Name: listHostGrantsToolName, Arguments: json.RawMessage(`{}`)}
	options := SessionRunnerChatOptions{SessionID: f.stream.SessionID, RunnerID: f.claim.RunnerID}
	if err := f.server.checkpointChatModelToolCallsWithContext(ctx, options, run, []agentruntime.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := f.server.checkpointSessionRunnerToolEvent(ctx, options, run, agentruntime.Event{
		Type: agentruntime.EventToolStarted, ToolCallID: callID, ToolName: call.Name, Arguments: string(call.Arguments),
	}); err != nil {
		t.Fatal(err)
	}
	return ctx, run, call
}

func TestRunnerDirectoryContextUsesClaimedInputAndLatestFailedDiscovery(t *testing.T) {
	f, run, request := directoryProjectionFixture(t)
	before, err := f.repo.ReadClaimedToolDiscovery(t.Context(), f.claim, listHostGrantsToolName)
	if err != nil || before.State != "completed" || before.Input == nil {
		t.Fatalf("canonical original discovery unavailable: %v", err)
	}
	appendSkillControlInput(t, f, "directory-queued-input", "LATER_QUEUED_INPUT", false)
	ctx, toolRun, call := beginDirectoryContextTool(t, f, "directory-latest-failure")
	if err := f.server.checkpointSessionRunnerToolEvent(ctx, SessionRunnerChatOptions{SessionID: f.stream.SessionID, RunnerID: f.claim.RunnerID}, toolRun, agentruntime.Event{
		Type: agentruntime.EventToolFailed, ToolCallID: call.ID, ToolName: call.Name,
		Arguments: string(call.Arguments), Result: `{"ok":false,"error":"Synthetic discovery failure."}`,
	}); err != nil {
		t.Fatal(err)
	}
	latest, err := f.repo.ReadClaimedToolDiscovery(t.Context(), f.claim, listHostGrantsToolName)
	if err != nil || latest.State != "failed" || latest.EventID <= before.EventID || latest.Input.EventID != before.Input.EventID ||
		latest.Input.Association.ClaimedInputRevision != f.claim.ClaimedInputRevision {
		t.Fatalf("reader fell back to old success or borrowed a queued input: latest=%+v err=%v", latest, err)
	}
	prepared, err := newRunnerDirectoryContext(f.server, run).prepare(t.Context(), request)
	if err != nil || directoryNoticeCount(prepared, directoryUnknownNotice) != 1 {
		t.Fatalf("a failed latest discovery was treated as current success: %v", err)
	}
	for _, message := range prepared.Messages {
		if strings.Contains(message.Content, "LATER_QUEUED_INPUT") {
			t.Fatal("later queued input leaked through optional discovery")
		}
	}
}

func TestRunnerDirectoryContextDetectsOmittedCurrentDiscovery(t *testing.T) {
	f, run, request := directoryProjectionFixture(t)
	request.Messages = request.Messages[:1]
	prepared, err := newRunnerDirectoryContext(f.server, run).prepare(t.Context(), request)
	if err != nil || directoryNoticeCount(prepared, directoryMissingNotice) != 1 {
		t.Fatalf("retained inline proof was confused with never-observed or visible discovery: %v", err)
	}
}
