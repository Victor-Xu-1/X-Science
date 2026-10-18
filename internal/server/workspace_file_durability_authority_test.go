package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspacePublicationRechecksCancellationAndAuthorityAfterDirectorySync(t *testing.T) {
	for _, phase := range []string{"cancelled", "authority_revoked"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			rootPath := t.TempDir()
			root, err := os.OpenRoot(rootPath)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			revoked := false
			rejection := errors.New("controlled post-sync authority rejection")
			commit := func(action func() error) error {
				if revoked {
					return rejection
				}
				return action()
			}
			stage, err := stageWorkspaceFileWithCommit(ctx, root, strings.NewReader("retained bytes"), 1024, commit, func(directory *os.File) error {
				if err := directory.Sync(); err != nil {
					return err
				}
				if phase == "cancelled" {
					cancel(rejection)
				} else {
					revoked = true
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer stage.close()
			if err := stage.publishWithCommit(ctx, "evidence.txt", commit); !errors.Is(err, rejection) {
				t.Fatalf("post-sync %s was accepted: %v", phase, err)
			}
			if got, err := os.ReadFile(filepath.Join(rootPath, "evidence.txt")); err != nil || string(got) != "retained bytes" {
				t.Fatalf("post-publication rejection discarded bytes: %q %v", got, err)
			}
		})
	}
}
