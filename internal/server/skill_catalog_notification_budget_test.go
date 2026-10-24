package server

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
	transcriptstore "x-science/internal/persistence/transcript"
)

type catalogNotificationBudgetLog struct {
	mu   sync.Mutex
	text strings.Builder
}

func (output *catalogNotificationBudgetLog) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.text.Write(data)
}

func (output *catalogNotificationBudgetLog) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.text.String()
}

func TestSkillCatalogOptionalNotificationCannotHoldCompletedPublicationBehindBusyPool(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "catalog-notification-budget-fixture"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Bounded notification fixture.")); err != nil {
		t.Fatal(err)
	}
	draft, found, err := fixture.server.findCompatibilitySkillDraft(name)
	if err != nil || !found {
		t.Fatalf("draft found=%t err=%v", found, err)
	}
	snapshot, err := captureSkillDraftSnapshot(draft)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := fixture.server.kernelSkillMutationCommit(t.Context(), fixture.bound, fixture.identity.access)
	if err != nil {
		t.Fatal(err)
	}
	var output catalogNotificationBudgetLog
	originalWriter := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(originalWriter)
	held, release, holderStarted, holderExited := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	holdContext, cancelHold := context.WithCancel(t.Context())
	var releaseOnce sync.Once
	releaseHold := func() { releaseOnce.Do(func() { close(release) }) }
	var holderError error
	defer func() {
		releaseHold()
		cancelHold()
		select {
		case <-holderStarted:
			select {
			case <-holderExited:
			case <-time.After(5 * time.Second):
				t.Error("same-store connection holder did not finish cleanup")
			}
		default:
		}
	}()
	unknown := kernelruntime.NewHostCallError("outcome_unknown", "synthetic acknowledgement lost after the real guard returned")
	calls := 0
	commit := func(action func() error) error {
		calls++
		if err := guard(action); err != nil {
			return err
		}
		// This is the repository returned by Store.TranscriptRepository. It
		// borrows the Store's real one-connection writer pool, not fixture.db.
		go func() {
			close(holderStarted)
			_, holderError = fixture.repo.RunImmediateWithOutcome(holdContext, func(*transcriptstore.ImmediateTransaction) error {
				close(held)
				select {
				case <-release:
					return nil
				case <-holdContext.Done():
					return holdContext.Err()
				}
			})
			close(holderExited)
		}()
		select {
		case <-held:
			return unknown
		case <-holderExited:
			return holderError
		case <-holdContext.Done():
			return holdContext.Err()
		}
	}
	type publicationResult struct {
		name string
		err  error
	}
	completed := make(chan publicationResult, 1)
	go func() {
		// Match the real HTTP/Host publication boundary's outer mutex.
		fixture.server.skillMutationMu.Lock()
		installed, publishError := fixture.server.publishCompatibilitySkillDraftWithCommit(name, false, snapshot.revision, commit)
		fixture.server.skillMutationMu.Unlock()
		completed <- publicationResult{name: installed.Name, err: publishError}
	}()
	select {
	case <-held:
	case <-holderExited:
		t.Fatalf("same-store writer could not be occupied after the real guard: %v", holderError)
	case <-time.After(5 * time.Second):
		t.Fatal("the real guard did not reach the controlled post-commit pool hold")
	}
	var result publicationResult
	select {
	case result = <-completed:
	case <-time.After(4 * time.Second):
		// Release first so the unfixed helper produces an ordinary assertion
		// failure instead of leaking a blocked goroutine or hanging the package.
		releaseHold()
		select {
		case <-completed:
		case <-time.After(5 * time.Second):
			t.Fatal("publication did not recover after releasing the same-store pool")
		}
		t.Fatal("optional catalog notification held a completed publication beyond its independent budget")
	}
	if !errors.Is(result.err, unknown) || result.name != "" || calls != 1 {
		t.Fatalf("notification changed/retried the original publication outcome: name=%q calls=%d err=%v", result.name, calls, result.err)
	}
	select {
	case <-holderExited:
		t.Fatal("the holder released the writer before the notification budget was observed")
	default:
	}
	if !strings.Contains(output.String(), "Skill catalog invalidation could not be persisted:") || !strings.Contains(output.String(), context.DeadlineExceeded.Error()) {
		t.Fatalf("notification did not record its real pool-acquisition timeout: %s", output.String())
	}
	if _, visible := findCatalogSkill(fixture.server.skillCatalog, name); !visible {
		t.Fatal("optional notification failure hid the installed Skill")
	}
	if _, err := os.Stat(filepath.Join(fixture.server.fileRoot, "skills", name, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	releaseHold()
	select {
	case <-holderExited:
		if holderError != nil {
			t.Fatal(holderError)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("same-store writer was not released")
	}
	if events := catalogChangeEvents(t, fixture.server, fixture.stream.OwnerID, 0); len(events) != 0 {
		t.Fatalf("a pool-blocked timed-out notification persisted later: %d", len(events))
	}
	if _, err := fixture.call("host.skills.read", "local:"+name); err != nil {
		t.Fatalf("the completed publication/read lock was not usable afterward: %v", err)
	}
	fixture.server.notifySkillCatalogChanged("")
	if events := catalogChangeEvents(t, fixture.server, fixture.stream.OwnerID, 0); len(events) != 1 || events[0].UserID != "" {
		t.Fatalf("notification could not use the released Store writer: %+v", events)
	}
}
