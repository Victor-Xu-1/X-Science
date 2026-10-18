package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestWebInputDurabilityFailureCannotAcceptCanonicalMessage(t *testing.T) {
	for _, phase := range []string{"namespace", "published"} {
		t.Run(phase, func(t *testing.T) {
			app, store := newP3WebConversationServer(t)
			repo, err := store.TranscriptRepository(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			app.transcriptStore = repo
			project := createP3Project(t, store, "durability-project", "local")
			frame, err := store.CreateFrame(workspace.CreateFrameInput{
				ID: "durability-frame", ProjectID: project.ID, AgentName: "OPERON", Status: "completed", ConversationType: "agent",
			})
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "evidence.txt")
			if err := os.WriteFile(source, []byte("retained fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			failures := 0
			app.workspaceDirectorySyncer = func(directory *os.File) error {
				if !app.hostGrantKernelMu.TryLock() {
					t.Fatal("directory sync ran while the host-grant mutation lock was held")
				}
				app.hostGrantKernelMu.Unlock()
				names, err := directory.Readdirnames(-1)
				if err != nil {
					return err
				}
				if phase == "namespace" || slices.Contains(names, "evidence.txt") {
					failures++
					return errors.New("controlled filesystem durability rejection")
				}
				return directory.Sync()
			}
			body := map[string]any{"content": "Retain the selected fixture.", "files": []string{source}, "loading_id": "durability-message"}
			response := p3JSONRequest(t, app, http.MethodPost, "/api/conversations/"+frame.ID+"/messages", body, "local")
			if response.Code != http.StatusServiceUnavailable || failures != 1 {
				t.Fatalf("unconfirmed filesystem became accepted: phase=%s status=%d failures=%d body=%s", phase, response.Code, failures, response.Body.String())
			}
			stream, found, err := repo.GetFrameStreamBySession(context.Background(), "local", frame.ID)
			if err != nil || (found && stream.InputRevision != 0) {
				t.Fatalf("failed publication committed canonical input: found=%t stream=%#v err=%v", found, stream, err)
			}
			app.workspaceDirectorySyncer = nil
			retry := p3JSONRequest(t, app, http.MethodPost, "/api/conversations/"+frame.ID+"/messages", body, "local")
			if retry.Code != http.StatusAccepted {
				t.Fatalf("same input could not recover after real durability became available: %d %s", retry.Code, retry.Body.String())
			}
			stream, found, err = repo.GetFrameStreamBySession(context.Background(), "local", frame.ID)
			if err != nil || !found || stream.InputRevision != 1 {
				t.Fatalf("recovery did not admit exactly one input: found=%t stream=%#v err=%v", found, stream, err)
			}
			if got, err := os.ReadFile(source); err != nil || string(got) != "retained fixture" {
				t.Fatalf("source file was changed: %q %v", got, err)
			}
		})
	}
}
