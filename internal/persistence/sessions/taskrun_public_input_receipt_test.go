package sessions

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestTaskRunPublicInputReceiptExactClaimPersistenceAndReset(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	if err := store.Save(Session{ID: "taskrun:public", LastRole: "user", LastUserMessageAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimRunner("taskrun:public", "public-runner", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim=%t err=%v", ok, err)
	}
	claim := RunnerClaimFromSession(claimed)
	receipt := RunnerInputReceipt{UserEventID: 7, TaskRunID: "public", TaskRunInstanceID: "server-instance"}
	bound, err := store.BindRunnerInputReceipt(claim, receipt)
	if err != nil || bound.Runner.InputReceipt == nil || *bound.Runner.InputReceipt != receipt {
		t.Fatalf("bound=%#v err=%v", bound, err)
	}
	reopened := NewStore(root)
	persisted, _, _ := reopened.Get(claim.SessionID)
	if !reflect.DeepEqual(persisted.Runner.InputReceipt, &receipt) {
		t.Fatalf("restart receipt=%#v", persisted.Runner.InputReceipt)
	}
	if _, err := reopened.BindRunnerInputReceipt(claim, receipt); err != nil {
		t.Fatalf("idempotent receipt: %v", err)
	}
	if _, err := reopened.BindRunnerInputReceipt(claim, RunnerInputReceipt{UserEventID: 8}); err == nil {
		t.Fatal("claim receipt changed to later ordinary input")
	}
	persisted.Runner.ClaimedAt = persisted.Runner.ClaimedAt.Add(-time.Hour)
	persisted.Runner.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	if err := reopened.Save(persisted); err != nil {
		t.Fatal(err)
	}
	replacement, ok, err := reopened.ClaimRunner(claim.SessionID, claim.RunnerID, time.Minute)
	if err != nil || !ok || replacement.Runner.InputReceipt != nil {
		t.Fatalf("reclaim receipt=%#v claimed=%t err=%v", replacement.Runner, ok, err)
	}
	if _, err := reopened.BindRunnerInputReceipt(claim, receipt); !errors.Is(err, ErrRunnerClaimStale) {
		t.Fatalf("stale claim bound receipt: %v", err)
	}
	ordinary := RunnerInputReceipt{UserEventID: 8}
	if _, err := reopened.BindRunnerInputReceipt(RunnerClaimFromSession(replacement), ordinary); err != nil {
		t.Fatalf("known ordinary receipt blocked: %v", err)
	}
}
