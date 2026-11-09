package runtimekv

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

type observedListContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (ctx *observedListContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}

func TestStoreListReadOnlyContextCancelsWhileBusy(t *testing.T) {
	for _, resource := range []string{"store_mutex", "connection_pool"} {
		t.Run(resource, func(t *testing.T) {
			store := runtimeStoreFixture(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
			if _, err := store.Set("fixture", "key", "preserved"); err != nil {
				t.Fatal(err)
			}
			var release func()
			if resource == "store_mutex" {
				held, unlock, holderDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
				go func() {
					holderDone <- store.EditNamespace("holder", func(map[string]Entry) (bool, error) {
						close(held)
						<-unlock
						return false, nil
					})
				}()
				<-held
				var once sync.Once
				release = func() { once.Do(func() { close(unlock); <-holderDone }) }
			} else {
				conn, err := store.db.Conn(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				var once sync.Once
				release = func() { once.Do(func() { _ = conn.Close() }) }
			}
			t.Cleanup(release)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			caller := &observedListContext{Context: ctx, observed: make(chan struct{})}
			started, done := make(chan struct{}), make(chan error, 1)
			go func() {
				close(started)
				_, err := store.ListReadOnlyContext(caller, "fixture")
				done <- err
			}()
			<-started
			// With the resource unavailable, observing Done establishes that the
			// read reached a cancellable wait before cancellation is triggered.
			select {
			case <-caller.observed:
			case <-time.After(time.Second):
				t.Error("blocked list never consulted the caller cancellation channel")
			}
			cancel()
			// The resource remains held until the read returns. This is a deadlock
			// watchdog, not an elapsed-time or scheduling-performance assertion.
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("cancelled list error = %v", err)
				}
			case <-time.After(time.Second):
				t.Error("cancelled list waited for the held " + resource)
				release()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("list did not join after releasing its isolated resource")
				}
			}
			release()
			entries, err := store.ListReadOnly("fixture")
			if err != nil || len(entries) != 1 || entries[0].Value != "preserved" || entries[0].Version != 1 {
				t.Fatalf("cancelled read changed durable state: entries=%+v err=%v", entries, err)
			}
		})
	}
}

func TestStoreListReadOnlyContextPreservesSnapshotContract(t *testing.T) {
	store := runtimeStoreFixture(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
	for _, seed := range []struct{ namespace, key string }{{"z", "b"}, {"a", "b"}, {"a", "a"}} {
		if _, err := store.Set(seed.namespace, seed.key, map[string]any{"value": seed.key}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.Exec(`PRAGMA busy_timeout=321`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	for _, caller := range []context.Context{nil, context.Background(), ctx} {
		for _, namespace := range []string{"", "a", "missing"} {
			want, err := store.List(namespace)
			if err != nil {
				t.Fatal(err)
			}
			got, err := store.ListReadOnlyContext(caller, namespace)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("namespace %q: got=%+v want=%+v err=%v", namespace, got, want, err)
			}
			if len(got) > 0 {
				got[0].Value.(map[string]any)["value"] = "mutated"
				again, err := store.ListReadOnlyContext(caller, namespace)
				if err != nil || !reflect.DeepEqual(again, want) {
					t.Fatalf("snapshot values are not detached: %+v err=%v", again, err)
				}
			}
		}
	}
	all, err := store.ListReadOnlyContext(ctx, "")
	if err != nil || len(all) != 3 || all[0].Namespace != "a" || all[0].Key != "a" || all[1].Key != "b" || all[2].Namespace != "z" {
		t.Fatalf("namespace/key order changed: %+v err=%v", all, err)
	}
	if _, err := store.ListReadOnlyContext(ctx, "../unsafe"); err == nil {
		t.Fatal("unsafe namespace accepted")
	}
	var timeout int
	if err := store.db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil || timeout != 321 {
		t.Fatalf("borrowed connection policy changed: timeout=%d err=%v", timeout, err)
	}
	cancel()
	if entries, err := store.ListReadOnlyContext(ctx, "a"); !errors.Is(err, context.Canceled) || len(entries) != 0 {
		t.Fatalf("pre-cancelled list: entries=%+v err=%v", entries, err)
	}
}
