package server

import (
	"context"
	"sync"
	"testing"
	"time"

	transcriptstore "synon-go/internal/persistence/transcript"
)

func TestSessionRunnerChatLoopClaimsCommittedInputWithoutIdlePollDelay(t *testing.T) {
	for _, commitDuringClaim := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle_wait", true: "query_to_wait_race"}[commitDuringClaim], func(t *testing.T) {
			store, repo, _ := newTranscriptWebFixture(t)
			seedTranscriptWebFrame(t, store, "local", "input-wake-project", "input-wake-frame")
			app := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
			t.Cleanup(func() { closeTestServer(t, app) })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			empty, release := make(chan struct{}), make(chan struct{})
			claimed, done := make(chan time.Time, 1), make(chan error, 1)
			var emptyOnce, releaseOnce sync.Once
			releaseClaim := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseClaim()
			cycle := func(ctx context.Context, options SessionRunnerChatOptions) (SessionRunnerCycleResult, error) {
				result, err := repo.ClaimNextFrameRunner(ctx, transcriptstore.ClaimNextFrameRunnerInput{
					RunnerID: options.RunnerID, TTL: time.Minute,
				})
				if err != nil {
					return SessionRunnerCycleResult{}, err
				}
				if result.Claimed {
					claimed <- time.Now()
					cancel()
					return SessionRunnerCycleResult{Claimed: true}, nil
				}
				emptyOnce.Do(func() {
					close(empty)
					if commitDuringClaim {
						select {
						case <-release:
						case <-ctx.Done():
						}
					}
				})
				return SessionRunnerCycleResult{}, nil
			}
			go func() {
				done <- app.runSessionRunnerChatLoop(ctx, normalizeSessionRunnerChatOptions(SessionRunnerChatOptions{
					RunnerID: "input-wake-runner", PollInterval: 30 * time.Second,
				}), cycle)
			}()
			t.Cleanup(func() {
				cancel()
				releaseClaim()
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("runner loop: %v", err)
					}
				case <-time.After(3 * time.Second):
					t.Error("runner loop did not stop")
				}
			})
			select {
			case <-empty:
			case <-time.After(3 * time.Second):
				t.Fatal("runner never reached empty claim")
			}
			started := time.Now()
			if _, _, err := app.submitFrameMessage(store, frameMessageSubmission{
				FrameID: "input-wake-frame", MessageUUID: "input-wake-message", ClientMessageID: "input-wake-message", Text: "Reply ready.",
			}); err != nil {
				t.Fatal(err)
			}
			releaseClaim()
			select {
			case at := <-claimed:
				t.Logf("committed input claimed in %s with 30s fallback polling", at.Sub(started))
			case <-time.After(2 * time.Second):
				t.Fatal("committed Transcript input did not wake the idle runner")
			}
		})
	}
}
