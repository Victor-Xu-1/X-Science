package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/skills"
)

func appendSkillControlInput(t *testing.T, f *agentSaveArtifactsFixture, id, text string, typed bool) transcriptstore.Event {
	t.Helper()
	var input map[string]any
	if typed {
		input = map[string]any{"turn_intent": conversationSkillControlIntent}
	}
	event, _, created, err := f.repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: id,
		FrameEventID: id + "-event", MessageUUID: id + "-message", Text: text,
		InputData: input, Destinations: []string{"ws"},
	})
	if err != nil || !created {
		t.Fatalf("append %s: created=%t err=%v", id, created, err)
	}
	f.stream, err = f.repo.GetStream(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func finishSkillControlInput(t *testing.T, f *agentSaveArtifactsFixture) {
	t.Helper()
	_, _, _, err := f.repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{
		Claim: f.claim, ClientMessageID: fmt.Sprintf("finish-%d", f.claim.Attempt), Status: "completed",
		PayloadJSON: []byte(`{"status":"completed","detail":"Recorded input completed."}`),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func claimSkillControlInput(t *testing.T, f *agentSaveArtifactsFixture) *sessionRunnerChatRun {
	t.Helper()
	claimed, err := f.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: "skill-control-runner",
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	f.claim = claimed.Claim
	return &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt),
		ClaimToken: f.claim.ClaimToken, Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
}

func loadSkillControlCatalog(t *testing.T, f *agentSaveArtifactsFixture) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"customize", "skill-creator"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: Test package authoring instructions.\n---\nInspect evidence; never claim an unpublished draft was published.\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.server.skillCatalog = skills.Load([]string{root})
}
