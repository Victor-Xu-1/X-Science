package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runtimekv "synon-go/internal/persistence/runtimekv"
	transcriptstore "synon-go/internal/persistence/transcript"
	workspace "synon-go/internal/persistence/workspace"
	"synon-go/internal/providers"
)

func cloneReplyForTest(t *testing.T, f *agentSaveArtifactsFixture, source string, attempt int64) string {
	t.Helper()
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/clone", map[string]any{
		"conversation": map[string]any{"id": source}, "through_attempt": attempt,
	}, f.stream.OwnerID)
	if response.Code != http.StatusCreated {
		frame, _, _ := f.store.GetCompatibilityFrame(source)
		_, err := f.store.CloneConversationWithTranscript(context.Background(), workspace.CloneConversationInput{OwnerUserID: f.stream.OwnerID, SourceFrameID: source, ExpectedSourceIncarnationID: frame.IncarnationID, TargetFrameID: "debug-inheritance", TargetName: "debug", ThroughAttempt: attempt})
		t.Fatalf("clone: %d %s cause=%v", response.Code, response.Body.String(), err)
	}
	return webString(p3DecodeObject(t, response)["id"])
}

func TestWebReplyBranchInheritedFilesAndUsage(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	f.server.runtimeStore = runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	record := func(session string, attempt int64, model string) {
		now := time.Now().UTC()
		f.server.recordSessionRunnerModelAudit(session, int(attempt), providers.AuditRecord{
			Protocol: providers.ProtocolOpenAICompatible, Model: model, HTTPStatus: 200,
			StartedAt: now, FinishedAt: now, PromptTokens: 100, CacheReadTokens: 80, CompletionTokens: 10, TotalTokens: 110,
		})
	}
	first := saveReplyBranchArtifact(t, f, "inherited-first", "inherited.txt")
	if err := f.store.PublishArtifactVersion(context.Background(), webString(first["version_id"]), webString(first["artifact_id"]), f.stream.ProjectID, f.stream.OwnerID); err != nil {
		t.Fatal(err)
	}
	record(f.stream.SessionID, f.claim.Attempt, "original-model")
	finishRoundPresentation(t, f, "completed")
	firstAttempt, original := f.claim.Attempt, f.stream
	child := cloneReplyForTest(t, f, original.FrameID, firstAttempt)
	startNextPresentationRound(t, f)
	later := saveReplyBranchArtifact(t, f, "inherited-later", "inherited.txt")
	if first["artifact_id"] != later["artifact_id"] || first["version_id"] == later["version_id"] {
		t.Fatalf("fixture must update the same artifact: %v %v", first, later)
	}
	record(original.SessionID, f.claim.Attempt, "later-parent-model")
	finishRoundPresentation(t, f, "completed")
	assertInherited := func(id string) {
		t.Helper()
		page := p3JSONRequest(t, f.server, http.MethodGet, "/api/conversations/"+id+"/messages?limit=80", nil, original.OwnerID)
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `"call_count":1`) || !strings.Contains(page.Body.String(), `"original-model"`) || strings.Contains(page.Body.String(), "later-parent-model") {
			t.Errorf("inherited usage: %d %s", page.Code, page.Body.String())
		}
		resolved := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+id+"/artifacts", map[string]any{
			"references": []any{map[string]any{"artifact_id": first["artifact_id"], "version_id": first["version_id"]}, map[string]any{"artifact_id": later["artifact_id"], "version_id": later["version_id"]}},
		}, original.OwnerID)
		if resolved.Code != http.StatusOK || !strings.Contains(resolved.Body.String(), webString(first["version_id"])) || strings.Contains(resolved.Body.String(), webString(later["version_id"])) || !strings.Contains(resolved.Body.String(), "inherited.txt") {
			t.Errorf("exact inherited metadata: %d %s", resolved.Code, resolved.Body.String())
		}
		library := p3JSONRequest(t, f.server, http.MethodGet, "/api/conversations/"+id+"/workspace?path=project-files&limit=1", nil, original.OwnerID)
		if library.Code != http.StatusOK || !strings.Contains(library.Body.String(), webString(first["version_id"])) || strings.Contains(library.Body.String(), webString(later["version_id"])) || !strings.Contains(library.Body.String(), `"read_only":true`) {
			t.Errorf("inherited library: %d %s", library.Code, library.Body.String())
		}
		foreign := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+id+"/artifacts", map[string]any{"version_ids": []any{first["version_id"]}}, "foreign-owner")
		if foreign.Code == http.StatusOK {
			t.Error("foreign owner read inherited file metadata")
		}
	}
	assertInherited(child)
	nested := cloneReplyForTest(t, f, child, firstAttempt)
	assertInherited(nested)
	// New child work uses its own receipts, even if the parent reused that
	// attempt number later. No provider request or model dispatch is needed.
	stream, found, err := f.repo.GetFrameStreamBySession(context.Background(), original.OwnerID, child)
	if err != nil || !found {
		t.Fatal(err)
	}
	f.stream = stream
	startNextPresentationRound(t, f)
	record(child, f.claim.Attempt, "child-model")
	finishRoundPresentation(t, f, "completed")
	summaries, err := f.server.completedRoundSummaries(context.Background(), stream.UID, stream.OwnerID, child, []int64{firstAttempt, f.claim.Attempt})
	if err != nil || summaries[firstAttempt].CallCount != 1 || summaries[f.claim.Attempt].CallCount != 1 || strings.Join(summaries[f.claim.Attempt].Models, ",") != "child-model" {
		t.Fatalf("child/source audit isolation: %+v %v", summaries, err)
	}
	if _, err := f.repo.CompletedRoundUsageAuthorities(context.Background(), stream.UID, "foreign", []int64{firstAttempt}); err == nil {
		t.Fatal("foreign usage authority accepted")
	}
	base, err := f.repo.GetBranchState(context.Background(), stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ForkFrameUserMessageBranch(context.Background(), transcriptstore.ForkFrameUserMessageBranchInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, SourceBranchID: base.ActiveBranchID,
		ExpectedActiveBranchID: base.ActiveBranchID, ExpectedGeneration: base.Generation,
		ClientMutationID: "edit-child-first", SourceClientMessageID: "save-user", SourceMessageIndex: 0,
		ReplacementText: "Different child branch", Destinations: []string{"ws"},
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := f.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "edited-child-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatal("edited child claim", err)
	}
	f.claim = claim.Claim
	finishRoundPresentation(t, f, "completed")
	inactive := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/clone", map[string]any{
		"conversation": map[string]any{"id": child}, "through_attempt": firstAttempt, "source_branch_id": base.ActiveBranchID,
	}, stream.OwnerID)
	if inactive.Code != http.StatusCreated {
		t.Fatalf("nested inactive branch: %d %s", inactive.Code, inactive.Body.String())
	}
	assertInherited(webString(p3DecodeObject(t, inactive)["id"]))
}
