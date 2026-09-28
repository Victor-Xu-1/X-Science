package server

import (
	"context"
	"testing"
)

func TestToolArtifactPresentationUsesGenerationReceiptNotFinalSummary(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	write := writeAgentSaveArtifactsFile(t, f.projectPath, "result.txt", "real generated output")
	f.saveExecution(t, f.identity.access, f.projectPath, "generation", 1, write)
	input := map[string]any{"files": []any{"result.txt"}, "destination": map[string]any{"result.txt": "snapshot"}, "language": "text", "human_description": "Save generated result"}
	saved, err := f.server.executeAgentSaveArtifacts(f.toolContext(t, "save-origin", input), f.identity, "save-origin", input)
	if err != nil {
		t.Fatal(err)
	}
	artifact := agentSaveArtifactResults(t, saved)[0]
	tool := func(call string) map[string]any {
		return map[string]any{"type": "tool_call", "content": map[string]any{"call_id": call, "name": "save_artifacts", "attempt": f.claim.Attempt}}
	}
	origin, unrelated := tool("save-origin"), tool("different-save")
	messages := []map[string]any{origin, unrelated}
	if err := f.server.enrichTranscriptWebArtifactPresentation(context.Background(), f.stream.FrameID, messages); err != nil {
		t.Fatal(err)
	}
	refs := transcriptWebArtifactReferenceMaps(origin["artifact_refs"])
	if len(refs) != 1 || webString(refs[0]["version_id"]) != stringValue(artifact["version_id"]) {
		t.Fatalf("generated file not attached to its actual tool message: %#v", refs)
	}
	if transcriptWebMessageHasArtifactReferences(unrelated["artifact_refs"]) {
		t.Fatal("same-name tool without a commit gained an unrelated attachment")
	}
	live := map[string]any{"data": origin["content"]}
	if err := f.server.enrichTranscriptToolStreamArtifacts(context.Background(), f.stream.UID, f.stream.OwnerID, live); err != nil {
		t.Fatal(err)
	}
	liveRefs := transcriptWebArtifactReferenceMaps(live["artifact_refs"])
	if len(liveRefs) != 1 || webString(liveRefs[0]["version_id"]) != webString(refs[0]["version_id"]) {
		t.Fatal("live delivery and reloaded history disagree on the generated version")
	}
	wrongAttempt := tool("save-origin")
	wrongAttempt["content"].(map[string]any)["attempt"] = f.claim.Attempt + 1
	if err := f.server.enrichTranscriptToolArtifacts(context.Background(), f.stream.UID, f.stream.OwnerID, []map[string]any{wrongAttempt}); err != nil {
		t.Fatal(err)
	}
	if transcriptWebMessageHasArtifactReferences(wrongAttempt["artifact_refs"]) {
		t.Fatal("a later attempt inherited a previous invocation's files")
	}
	again := tool("save-origin")
	if err := f.server.enrichTranscriptWebArtifactPresentation(context.Background(), f.stream.FrameID, []map[string]any{again}); err != nil {
		t.Fatal(err)
	}
	if len(transcriptWebArtifactReferenceMaps(again["artifact_refs"])) != 1 {
		t.Fatal("reloading duplicated or lost the origin attachment")
	}
	if err := f.server.enrichTranscriptToolArtifacts(context.Background(), f.stream.UID, "different-owner", messages); err == nil {
		t.Fatal("foreign owner obtained generation receipts")
	}
}
