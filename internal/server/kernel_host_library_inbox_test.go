package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

func TestKernelHostLibrarySaveToInboxStagesPendingMetadata(t *testing.T) {
	f := newKernelSkillDraftFixture(t)
	startDelegateInitializationBatch(t, f.agentSaveArtifactsFixture, f.bound.transcriptExecution.outerToolUseID)
	request := map[string]any{
		"operation_id": "2105335f-cd2b-46d6-82fd-dfcf75ee6225",
		"references": []any{map[string]any{"provider": "task", "external_id": "synthetic-745",
			"title": "Synthetic Library task reference", "metadata": map[string]any{"doi": "10.1234/task-inbox", "abstract": "Synthetic metadata only."}}},
	}
	result, err := f.call("host.library.save_to_inbox", request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Schema     string `json:"schema"`
		Candidates []struct {
			ID     string `json:"id"`
			State  string `json:"state"`
			ItemID string `json:"item_id"`
		} `json:"candidates"`
	}
	if err = json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Schema != "x-science.literature-task-inbox-receipt.v1" || len(receipt.Candidates) != 1 || receipt.Candidates[0].State != "pending" || receipt.Candidates[0].ItemID != "" {
		t.Fatalf("metadata staging must return pending, not accepted: %s", raw)
	}
	pending, err := f.store.ListLiteratureCandidates(context.Background(), f.identity.access.UserID, "pending", "")
	if err != nil || len(pending.Candidates) != 1 || pending.Candidates[0].ID != receipt.Candidates[0].ID {
		t.Fatalf("pending readback=%#v err=%v", pending, err)
	}
	if len(pending.Candidates[0].TaskOrigins) != 1 || pending.Candidates[0].TaskOrigins[0].ProjectID != f.identity.access.Frame.ProjectID {
		t.Fatalf("trusted task provenance=%#v", pending)
	}
}

func TestKernelHostLibraryCapabilityIsDiscoverable(t *testing.T) {
	if agentRuntimeToolNeedsApproval(librarySaveToInboxToolName) {
		t.Fatal("Library Inbox pending review must not become a task permission pause")
	}
	f := newKernelSkillDraftFixture(t)
	policy := f.server.agentKernelHostCallPolicy(context.Background(), f.identity.access, f.projectPath, nil, false, f.bound.transcriptExecution)
	allowed := false
	for _, method := range policy.AllowedMethods {
		if method == "host.library.save_to_inbox" {
			allowed = true
		}
	}
	if !allowed {
		t.Fatal("first-party Library save-to-Inbox is not advertised in Host policy")
	}
	result, err := policy.Handler(context.Background(), kernelruntime.HostCall{ID: "capabilities-745", Method: "host.capabilities"})
	if err != nil || mapValue(result)["host.library.save_to_inbox"] != true {
		t.Fatalf("capability=%#v err=%v", result, err)
	}
	schemas := partitionAgentRuntimeToolSchemasForModel(f.server.agentRuntimeToolSchemasWithContext(context.Background(), nil, f.stream.FrameID))
	advertised := false
	for _, schema := range schemas {
		if schema.Name == librarySaveToInboxToolName {
			advertised = true
			properties := mapValue(schema.Parameters["properties"])
			if properties["operation_id"] == nil || properties["references"] == nil || properties["owner_id"] != nil || properties["project_id"] != nil {
				t.Fatalf("untrusted authority advertised: %#v", schema)
			}
		}
	}
	if !advertised {
		t.Fatal("actual runtime discovery did not expose first-party save_to_inbox")
	}
	// The metadata-only scope is frozen before dispatch, even without a kernel
	// manager. Replacing a frame cannot retarget an old advertised operation.
	access := f.server.captureLibraryTaskAccess(t.Context(), f.stream.FrameID)
	if access == nil {
		t.Fatal("metadata scope incorrectly requires a Python runtime")
	}
	if err = f.store.DeleteFrame(f.stream.FrameID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.CreateFrame(workspace.CreateFrameInput{ID: f.stream.FrameID, ProjectID: f.stream.ProjectID, AgentName: "OPERON", Status: "processing", ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	ctx := withTranscriptRunnerChatRun(t.Context(), &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}})
	_, err = f.server.executeAgentLibraryInbox(ctx, access, "old-advertised-call", map[string]any{})
	var denied *kernelruntime.HostCallError
	if !errors.As(err, &denied) || denied.Code != "permission_denied" {
		t.Fatalf("old frame scope retargeted: %v", err)
	}
}

func TestLibraryTaskToolAuthorityRespectsExplicitScope(t *testing.T) {
	const frameID = "library-task-scope"
	store, repo, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "library-task-scope-project", frameID)
	server := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	if _, _, err := server.submitFrameMessage(store, frameMessageSubmission{
		FrameID: frameID, MessageUUID: "library-scope-message", ClientMessageID: "library-scope-input",
		Text: "Check the current task's Library capability.",
	}); err != nil {
		t.Fatal(err)
	}
	stream, found, err := repo.GetFrameStreamBySession(t.Context(), "local", frameID)
	if err != nil || !found {
		t.Fatalf("frame stream found=%t err=%v", found, err)
	}
	session, err := server.loadTranscriptFrameSessionProjection(stream)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		allowed []string
		want    bool
	}{
		{name: "ordinary task", want: true},
		{name: "explicit opt-in", allowed: []string{librarySaveToInboxToolName}, want: true},
		{name: "unrelated explicit scope", allowed: []string{"runtime_get"}},
		{name: "no tools", allowed: []string{noChatToolsAllowedSentinel}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authority, err := server.bindSessionRunnerToolAuthority(t.Context(), session, SessionRunnerChatOptions{
				AllowedTools: tc.allowed, DisableMCPDiscovery: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			admitted := agentKernelToolAllowed(librarySaveToInboxToolName, chatRunnerAllowedToolSet(authority.Options.AllowedTools))
			schemas := filterAgentRuntimeToolSchemas(authority.Schemas, authority.Options.AllowedTools)
			advertised := agentRuntimeToolSchemaNamed(partitionAgentRuntimeToolSchemasForModel(schemas), librarySaveToInboxToolName)
			if admitted != tc.want || advertised != tc.want {
				t.Fatalf("Library authority admitted=%t advertised=%t, want %t; scope=%v", admitted, advertised, tc.want, authority.Options.AllowedTools)
			}
		})
	}
}

func TestKernelHostLibraryHonorsTheSameExplicitToolFilter(t *testing.T) {
	f := newKernelSkillDraftFixture(t)
	startDelegateInitializationBatch(t, f.agentSaveArtifactsFixture, f.bound.transcriptExecution.outerToolUseID)
	for _, schema := range f.server.agentRuntimeToolSchemasWithContext(t.Context(), []string{"repl"}, f.stream.FrameID) {
		if schema.Name == librarySaveToInboxToolName {
			t.Error("native discovery ignored the explicit tool filter")
		}
	}
	policy := f.server.agentKernelHostCallPolicy(t.Context(), f.identity.access, f.projectPath, []string{"repl"}, false, f.bound.transcriptExecution)
	for _, method := range policy.AllowedMethods {
		if method == "host.library.save_to_inbox" {
			t.Error("SDK advertised an explicitly excluded Library capability")
		}
	}
	capabilities, err := policy.Handler(t.Context(), kernelruntime.HostCall{ID: "library-filter-capabilities", Method: "host.capabilities"})
	if err != nil || mapValue(capabilities)["host.library.save_to_inbox"] != false {
		t.Fatalf("filtered discovery=%#v err=%v", capabilities, err)
	}
	request := map[string]any{"operation_id": "72d379ff-b7f0-46ba-b542-d7a6b20fdd9e", "references": []any{
		map[string]any{"provider": "crossref", "external_id": "10.1234/filtered", "title": "Filtered reference", "metadata": map[string]any{}},
	}}
	_, err = policy.Handler(t.Context(), kernelruntime.HostCall{ID: "library-filter-write", Method: "host.library.save_to_inbox", Args: []any{request}})
	var denied *kernelruntime.HostCallError
	if !errors.As(err, &denied) || denied.Code != "permission_denied" {
		t.Fatalf("SDK bypassed native tool filter: %v", err)
	}
	pending, err := f.store.ListLiteratureCandidates(t.Context(), f.identity.access.UserID, "pending", "")
	if err != nil || len(pending.Candidates) != 0 {
		t.Fatalf("excluded capability changed Inbox: %#v %v", pending, err)
	}
}
