package server

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRuntimeOperationOwnershipReleasesOnce(t *testing.T) {
	server := New(Options{})
	first, err := server.beginRuntimeOperation()
	if err != nil {
		t.Fatal(err)
	}
	second, err := server.beginRuntimeOperation()
	if err != nil {
		t.Fatal(err)
	}
	var callers sync.WaitGroup
	for index := 0; index < 32; index++ {
		callers.Go(first)
	}
	callers.Wait()
	if sessions, operations, draining := server.runtimeActivity(); sessions != 0 || operations != 1 || draining {
		t.Fatalf("one release changed another owner's lifetime: %d/%d/%t", sessions, operations, draining)
	}
	if server.TryDrainIdle() {
		t.Fatal("a remaining owned operation did not block idle admission")
	}
	second()
	if !server.TryDrainIdle() {
		t.Fatal("released ownership continued to block idle admission")
	}
}

func TestRuntimeOperationWrapperReleasesOnReturnAndPanic(t *testing.T) {
	for _, panicHandler := range []bool{false, true} {
		server := New(Options{})
		recovered := false
		func() {
			defer func() { recovered = recover() != nil }()
			server.withRuntimeOperation(func(w http.ResponseWriter, r *http.Request) {
				if _, operations, _ := server.runtimeActivity(); operations != 1 {
					t.Error("wrapped operation was not owned")
				}
				if panicHandler {
					panic("synthetic handler panic")
				}
				w.WriteHeader(http.StatusBadRequest)
			})(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/fixture", nil))
		}()
		if recovered != panicHandler {
			t.Fatal("the wrapper swallowed or introduced a handler panic")
		}
		if _, operations, _ := server.runtimeActivity(); operations != 0 {
			t.Fatal("return/panic retained runtime ownership")
		}
	}
}

func TestRuntimeOperationRejectsAdmissionDuringDrainEvaluation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	server := New(Options{TryDrainIdleDurableWait: func() { close(entered); <-release }})
	drained := make(chan bool, 1)
	go func() { drained <- server.TryDrainIdle() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("drain evaluation did not begin")
	}
	called := false
	response := httptest.NewRecorder()
	server.withRuntimeOperation(func(http.ResponseWriter, *http.Request) { called = true })(
		response, httptest.NewRequest(http.MethodPost, "/fixture", nil))
	if response.Code != http.StatusServiceUnavailable || called {
		t.Fatal("new work crossed the in-progress atomic drain fence")
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case accepted := <-drained:
		if !accepted {
			t.Fatal("rejected new work prevented idle drain")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not settle")
	}
}
