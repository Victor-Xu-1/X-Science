package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"x-science/internal/persistence/runtimekv"
	"x-science/internal/persistence/workspace"
)

func TestWebContextUsageCancellationReleasesBlockedSnapshot(t *testing.T) {
	for _, suffix := range []string{"", "?inspect=1"} {
		t.Run("context-usage"+suffix, func(t *testing.T) {
			store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
			t.Cleanup(func() { _ = store.Close() })
			held, unlock, holderDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				holderDone <- store.EditNamespace("holder", func(map[string]runtimekv.Entry) (bool, error) {
					close(held)
					<-unlock
					return false, nil
				})
			}()
			<-held
			var once sync.Once
			release := func() { once.Do(func() { close(unlock); <-holderDone }) }
			t.Cleanup(release)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			request := httptest.NewRequest(http.MethodGet, "/context-usage"+suffix, nil).WithContext(ctx)
			response := httptest.NewRecorder()
			app := &Server{runtimeStore: store}
			started, done := make(chan struct{}), make(chan struct{})
			go func() {
				close(started)
				app.handleWebContextUsage(response, request, workspace.CompatibilityFrame{Frame: workspace.Frame{ID: "cancelled-fixture"}})
				close(done)
			}()
			<-started
			cancel()
			select {
			case <-done:
				if response.Code != http.StatusServiceUnavailable {
					t.Errorf("cancelled snapshot HTTP %d: %s", response.Code, response.Body.String())
				}
			case <-time.After(time.Second):
				t.Error("cancelled HTTP request waited for the held snapshot Store lock")
				release()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("handler did not join after releasing the isolated Store lock")
				}
			}
			release()
			if _, err := store.Set("after-cancellation", "key", "writable"); err != nil {
				t.Fatalf("cancelled read retained the Store lock: %v", err)
			}
		})
	}
}
