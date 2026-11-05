package transcript

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestTypedHistoryBootstrapReadUsesReadPoolAndPreservesAuthority(t *testing.T) {
	repository, writer, dsn := newTranscriptRepository(t)
	prepareHistoryInventoryFixture(t, writer)
	seedHistoryInventoryFrame(t, writer, "reader-owner", "reader-project", "reader-frame", "completed")
	if _, err := writer.Exec(`INSERT INTO frame_events(id,frame_id,sequence,event_type,payload,created_at) VALUES
		('reader-tool','reader-frame',1,'assistant_message',
		'{"role":"assistant","content":[{"type":"tool_use","id":"reader-call","name":"read_file","input":{}}]}',CURRENT_TIMESTAMP),
		('reader-result','reader-frame',2,'user_message',
		'{"role":"user","content":[{"type":"tool_result","tool_use_id":"reader-call","content":"ok"}]}',CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if report, err := repository.ReconcileNoStreamFrameHistories(t.Context(), ReconcileNoStreamFrameHistoriesInput{Limit: 10}); err != nil || report.PayloadCreated != 1 {
		t.Fatalf("bootstrap positive authority: created=%d err=%v", report.PayloadCreated, err)
	}
	reader, err := sql.Open("sqlite", dsn+"&mode=ro&_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	readRepository := NewRepositoryWithReadPool(writer, reader)
	writer.SetMaxOpenConns(1)
	held, err := writer.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	beforeWait := writer.Stats().WaitCount
	done := make(chan error, 1)
	go func() {
		for _, input := range []struct {
			stream, owner string
			epoch         int64
			want          bool
		}{
			{"frame:reader-frame", "reader-owner", 1, true},
			{"frame:reader-frame", "foreign-owner", 1, false},
			{"frame:reader-frame", "reader-owner", 2, false},
			{"missing-stream", "reader-owner", 1, false},
		} {
			found, err := readRepository.HasActiveTypedHistoryBootstrap(t.Context(), input.stream, input.owner, input.epoch)
			if err != nil || found != input.want {
				done <- errors.New("read pool changed exact bootstrap owner/stream/epoch result")
				return
			}
		}
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := readRepository.HasActiveTypedHistoryBootstrap(canceled, "frame:reader-frame", "reader-owner", 1)
		if !errors.Is(err, context.Canceled) {
			done <- errors.New("bootstrap read lost caller cancellation")
			return
		}
		done <- nil
	}()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	var result error
	for {
		select {
		case result = <-done:
			goto settled
		case <-deadline.C:
			t.Error("pure bootstrap read did not settle while writer connection was reserved")
			goto release
		default:
		}
		if writer.Stats().WaitCount > beforeWait {
			t.Error("pure bootstrap read entered the serialized writer pool")
			goto release
		}
		runtime.Gosched()
	}
release:
	_ = held.Close()
	select {
	case result = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("bootstrap read did not settle after writer cleanup")
	}
settled:
	_ = held.Close()
	if result != nil {
		t.Fatal(result)
	}
	if found, err := repository.HasActiveTypedHistoryBootstrap(t.Context(), "frame:reader-frame", "reader-owner", 1); err != nil || !found {
		t.Fatalf("legacy single-pool read changed: found=%t err=%v", found, err)
	}
}
