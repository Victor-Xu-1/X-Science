package server

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	runtimekv "x-science/internal/persistence/runtimekv"
)

func TestOptionalCommunicationMetadataDoesNotWaitForStore(t *testing.T) {
	for _, cancelRun := range []bool{true, false} {
		for _, operation := range []string{"published", "settled", "audit"} {
			t.Run(fmt.Sprintf("cancelled=%t/%s", cancelRun, operation), func(t *testing.T) {
				fixture := newAgentSaveArtifactsFixture(t)
				server, store := fixture.server, fixture.server.runtimeStore
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				run := &sessionRunnerChatRun{SessionID: fixture.stream.FrameID, Attempt: int(fixture.claim.Attempt),
					Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}}
				schedule, err := server.loadSessionRunnerCommunicationSchedule(ctx, run)
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan struct{})
				namespace := runnerCommunicationScheduleNamespace
				if operation == "audit" {
					namespace = sessionRunnerCommunicationAuditNamespace
				}
				// Hold the real store lock with a channel boundary, not a production
				// database or a sleeping writer. Cancellation must finish the
				// optional write before this callback releases that lock.
				err = store.EditNamespace("communication-cancellation-fixture", func(map[string]runtimekv.Entry) (bool, error) {
					if cancelRun {
						cancel()
					}
					go func() {
						defer close(done)
						switch operation {
						case "published":
							schedule.published()
						case "settled":
							schedule.settled()
						case "audit":
							server.recordSessionRunnerCommunicationAudit(ctx, run, map[string]any{"decision": "fixture-boundary"})
						}
					}()
					select {
					case <-done:
					case <-time.After(200 * time.Millisecond):
						t.Error("optional communication metadata outwaited its cancellation or finite write budget")
					}
					return false, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("metadata caller did not join after releasing the fixture lock")
				}
				if operation == "published" && schedule.LastPublished.IsZero() || operation == "settled" && schedule.CompletedSteps != 1 {
					t.Fatal("optional persistence failure discarded in-memory cadence progress")
				}
				entries, err := store.List(namespace)
				if err != nil || len(entries) != 0 {
					t.Fatalf("cancelled optional write ran late after releasing the lock: rows=%d err=%v", len(entries), err)
				}
			})
		}
	}
}

func TestCommunicationScheduleLoadBoundsOptionalStoreAndKeepsRestore(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	server, store := fixture.server, fixture.server.runtimeStore
	run := &sessionRunnerChatRun{SessionID: fixture.stream.FrameID, Attempt: int(fixture.claim.Attempt),
		Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}}
	// Warm only the store schema; this is not an extra cadence write.
	if _, _, err := store.Get(runnerCommunicationScheduleNamespace, "initial-fixture-read"); err != nil {
		t.Fatal(err)
	}
	type loadedSchedule struct {
		schedule *sessionRunnerCommunicationSchedule
		err      error
	}
	loaded := make(chan loadedSchedule, 1)
	var result loadedSchedule
	received := false
	err := store.EditNamespace("communication-load-fixture", func(map[string]runtimekv.Entry) (bool, error) {
		go func() {
			schedule, err := server.loadSessionRunnerCommunicationSchedule(t.Context(), run)
			loaded <- loadedSchedule{schedule, err}
		}()
		select {
		case result = <-loaded:
			received = true
		case <-time.After(200 * time.Millisecond):
			t.Error("optional cadence load waited indefinitely for the held store")
		}
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !received {
		select {
		case result = <-loaded:
		case <-time.After(time.Second):
			t.Fatal("cadence load caller did not join after releasing the fixture lock")
		}
	}
	if result.err != nil || result.schedule == nil || result.schedule.persist == nil {
		t.Fatalf("optional restore failure lost the runnable local schedule: %+v", result)
	}
	result.schedule.published()
	result.schedule.settled()
	restored, err := server.loadSessionRunnerCommunicationSchedule(t.Context(), run)
	if err != nil || restored.LastPublished != result.schedule.LastPublished || restored.CompletedSteps != 1 {
		t.Fatalf("healthy subsequent writes did not restore cadence: %+v err=%v", restored, err)
	}
}

func TestCommunicationScheduleDoesNotDowngradeBranchAuthorityError(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	stream := fixture.stream
	stream.OwnerID = "unrelated-fixture-owner"
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: fixture.claim}}
	if schedule, err := fixture.server.loadSessionRunnerCommunicationSchedule(t.Context(), run); err == nil || schedule != nil {
		t.Fatalf("branch authority failure became optional cadence fallback: schedule=%+v err=%v", schedule, err)
	}
}

func TestCommunicationScheduleWriteErrorKeepsLocalProgress(t *testing.T) {
	now := time.Now().UTC()
	schedule := &sessionRunnerCommunicationSchedule{now: func() time.Time { return now },
		persist: func(*sessionRunnerCommunicationSchedule) error {
			return errors.New("fixture optional store unavailable")
		}}
	schedule.published()
	schedule.settled()
	if schedule.LastPublished != now || schedule.CompletedSteps != 1 {
		t.Fatal("an optional store failure discarded already observed local progress")
	}
	now = now.Add(90 * time.Second)
	if !schedule.due() {
		t.Fatal("an optional store failure disabled the next real progress update")
	}
}
