package server

import (
	"testing"

	eventjournal "synon-go/internal/persistence/journal"
)

func TestSessionRunnerArtifactReferencesUseScopedImmutableReceipts(t *testing.T) {
	entries := []eventjournal.Entry{
		{EventID: 1, Message: eventjournal.Message{
			"role": "user", "type": "message", "messageOrigin": "task_intent", "text": "完成一个不相关的旧任务",
		}},
		{EventID: 2, Message: eventjournal.Message{
			"type": "runner_checkpoint", "status": "completed", "toolName": "web_search",
		}},
		{EventID: 3, ClientMessageID: "root-task-boundary", Message: eventjournal.Message{
			"role": "user", "type": "message", "messageOrigin": "task_intent", "text": "评估公开文献中的蛋白工程",
		}},
		{EventID: 4, Message: eventjournal.Message{
			"type": "runner_checkpoint", "status": "completed", "toolName": "mcp__pubmed__search_articles",
			"toolResult": map[string]any{"artifacts": []any{map[string]any{
				"artifact_id": "artifact-report", "version_id": "version-report",
			}}},
		}},
		{EventID: 5, Message: eventjournal.Message{
			"role": "user", "type": "message", "messageOrigin": "task_intent", "text": "继续当前报告，保留已有证据",
		}},
		{EventID: 6, Message: eventjournal.Message{
			"type": "runner_checkpoint", "status": "completed", "toolName": "python",
		}},
		{EventID: 7, Message: eventjournal.Message{
			"role": "user", "type": "message", "messageOrigin": "task_intent", "text": "继续当前报告，保留已有证据",
		}},
	}
	scoped, found := filterRunnerEntriesByLatestTaskIntent(entries[:5])
	if !found || len(scoped) != 1 || scoped[0].EventID != 5 {
		t.Fatalf("new authored request did not establish its execution scope: %#v", scoped)
	}
	if refs := artifactReferencesFromRunnerEntries(scoped); len(refs) != 0 {
		t.Fatalf("new request claimed previous outputs as its execution: %#v", refs)
	}
	refs := artifactReferencesFromRunnerEntries(entries[2:4])
	if len(refs) != 1 || refs[0].ArtifactID != "artifact-report" || refs[0].VersionID != "version-report" {
		t.Fatalf("continuation artifact references=%#v", refs)
	}
}

func TestSessionRunnerAuthoritativeSourceSignalPredicate(t *testing.T) {
	if sessionRunnerHasAuthoritativeSourceEvidence([]string{"execution-tool:python"}) {
		t.Fatal("runtime execution alone must not become source evidence")
	}
	if sessionRunnerHasAuthoritativeSourceEvidence([]string{"source-host:pubchem.ncbi.nlm.nih.gov"}) {
		t.Fatal("a host identity without a validated read receipt became source evidence")
	}
	for _, signal := range []string{
		"source-connector:bundled:chembl",
		trustedScientificValidatedPublicWebFetchSignal,
	} {
		if !sessionRunnerHasAuthoritativeSourceEvidence([]string{signal}) {
			t.Fatalf("trusted source signal was not recognized: %q", signal)
		}
	}
}
