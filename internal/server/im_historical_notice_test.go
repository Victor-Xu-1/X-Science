package server

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	adaptercommon "x-science/internal/adapters/common"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
)

const historicalNoticeEnglish = "Retained input and progress are preserved, but this older input cannot be resumed automatically with verified ordering. Send a new instruction to continue from the retained context."

func historicalNoticeServer(t *testing.T, root string, store *workspace.Store, repo *transcriptstore.Repository,
	stream transcriptstore.Stream, authorize func(adaptercommon.IMSessionBinding) error,
	handler adaptercommon.SessionOutboundHandler,
) (*Server, *adaptercommon.SessionOutboundDispatcher, <-chan error) {
	t.Helper()
	settled := make(chan error, 8)
	var app *Server
	dispatcher, err := adaptercommon.NewSessionOutboundDispatcher(t.Context(), adaptercommon.SessionOutboundDispatcherOptions{
		FlushInterval: time.Millisecond, Authorize: authorize,
		Delivery: adaptercommon.OutboundDeliveryOptions{MaxAttempts: 2, RetryDelay: time.Millisecond},
		Handlers: map[string]adaptercommon.SessionOutboundHandler{"wechat": handler},
		OnResult: func(result adaptercommon.SessionOutboundResult) { settled <- app.RecordIMOutboundResult(result) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTranscriptIMTestDispatcher(t, dispatcher) })
	app = New(Options{Workspace: store, Transcript: repo, FileRoot: root, IMOutbound: dispatcher})
	if _, err := app.pairingStore.Allow("wechat", "historical", "test user"); err != nil {
		t.Fatal(err)
	}
	if err := app.persistIMOutboundBindingState(adaptercommon.IMSessionBinding{SessionID: stream.SessionID, Platform: "wechat",
		ChatID: "historical", SenderID: "historical", TargetID: "historical", OwnerUserID: stream.OwnerID, ContextToken: "private-test-context"}); err != nil {
		t.Fatal(err)
	}
	return app, dispatcher, settled
}

func awaitHistoricalNotice(t *testing.T, settled <-chan error) {
	t.Helper()
	select {
	case err := <-settled:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("RecoverIMOutbound produced no acknowledged user-visible historical-input notice")
	}
}

func TestIMHistoricalInputRecoveryNoticeUsesOneDurableReceiptWithoutAdmission(t *testing.T) {
	store, repo, db, stream := historicalIMNoticeFixture(t, "completed")
	root := t.TempDir()
	var calls atomic.Int32
	app, dispatcher, settled := historicalNoticeServer(t, root, store, repo, stream, nil,
		func(_ context.Context, _ adaptercommon.IMSessionBinding, message adaptercommon.ServerMessage) (adaptercommon.OutboundReport, error) {
			if message["type"] != "content_delta" || message["text"] != historicalNoticeEnglish || strings.Contains(message["text"].(string), "private-test-context") {
				t.Errorf("notice did not reach common adapter text contract: %#v", message)
			}
			if calls.Add(1) == 1 {
				return adaptercommon.OutboundReport{}, errors.New("controlled channel retry")
			}
			return adaptercommon.OutboundReport{Platform: "wechat"}, nil
		})
	if _, err := app.RecoverIMOutbound(t.Context()); err != nil {
		t.Fatal(err)
	}
	awaitHistoricalNotice(t, settled)
	closeTranscriptIMTestDispatcher(t, dispatcher)
	// A real dispatcher restart and repeated recovery use the same stored receipt.
	app, _, _ = historicalNoticeServer(t, root, store, repo, stream, nil,
		func(context.Context, adaptercommon.IMSessionBinding, adaptercommon.ServerMessage) (adaptercommon.OutboundReport, error) {
			calls.Add(1)
			return adaptercommon.OutboundReport{}, nil
		})
	for range 3 {
		if _, err := app.RecoverIMOutbound(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	var notices, delivered, inputs, attempts, facts int
	if err := db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(i.status='delivered'),0) FROM transcript_events e
		JOIN transcript_delivery_intents i ON i.stream_uid=e.stream_uid AND i.publication_seq=e.publication_seq
		WHERE e.stream_uid=? AND e.event_type='host_recovery_notice'`, stream.UID).Scan(&notices, &delivered); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM transcript_events WHERE event_type IN ('user_message','user_input_response')),
		(SELECT COUNT(*) FROM transcript_runner_attempts),(SELECT COUNT(*) FROM transcript_input_admissions)`).Scan(&inputs, &attempts, &facts); err != nil {
		t.Fatal(err)
	}
	current, err := repo.GetStream(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || notices != 1 || delivered != 1 || calls.Load() != 2 || inputs != 2 || attempts != 1 || facts != 0 ||
		current.InputRevision != stream.InputRevision || current.ConsumedInputRevision != stream.ConsumedInputRevision {
		t.Fatalf("notice changed authority or repeated: notices=%d delivered=%d calls=%d inputs=%d attempts=%d facts=%d stream=%+v err=%v",
			notices, delivered, calls.Load(), inputs, attempts, facts, current, err)
	}
	claimed, err := repo.ClaimNextRunner(t.Context(), transcriptstore.ClaimNextRunnerInput{RunnerID: "must-not-run", TTL: time.Minute})
	if err != nil || claimed.Claimed {
		t.Fatalf("notice admitted old input: %+v err=%v", claimed, err)
	}
}

func TestIMHistoricalInputNoticeRevalidatesAfterQueueAndBeforeRetry(t *testing.T) {
	for _, transition := range []string{"queued_new_input", "queued_branch_generation", "retry_new_input"} {
		t.Run(transition, func(t *testing.T) {
			store, repo, db, stream := historicalIMNoticeFixture(t, "completed")
			ready, release := make(chan struct{}, 1), make(chan struct{})
			defer close(release)
			mutate := func() error {
				if transition == "queued_branch_generation" {
					// Standalone has no public branch-fork API. This isolated SQL
					// fixture changes only the read-side generation, not input proof.
					_, err := db.Exec(`UPDATE transcript_branch_state SET generation=generation+1 WHERE stream_uid=?`, stream.UID)
					return err
				}
				_, _, err := repo.AppendUserEvent(t.Context(), transcriptstore.AppendUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
					ClientMessageID: "new-ordinary-input", PayloadJSON: []byte(`{"text":"Continue with this new instruction"}`)})
				return err
			}
			var calls atomic.Int32
			authorize := func(adaptercommon.IMSessionBinding) error {
				if transition != "retry_new_input" {
					ready <- struct{}{}
					select {
					case <-release:
					case <-t.Context().Done():
						return t.Context().Err()
					}
				}
				return nil
			}
			app, _, settled := historicalNoticeServer(t, t.TempDir(), store, repo, stream, authorize,
				func(context.Context, adaptercommon.IMSessionBinding, adaptercommon.ServerMessage) (adaptercommon.OutboundReport, error) {
					calls.Add(1)
					if transition == "retry_new_input" {
						if err := mutate(); err != nil {
							t.Error(err)
						}
						return adaptercommon.OutboundReport{}, errors.New("controlled first attempt failed")
					}
					return adaptercommon.OutboundReport{}, nil
				})
			if _, err := app.RecoverIMOutbound(t.Context()); err != nil {
				t.Fatal(err)
			}
			if transition != "retry_new_input" {
				select {
				case <-ready:
				case <-time.After(time.Second):
					t.Fatal("no recovery notice reached the real queued consumer")
				}
				if err := mutate(); err != nil {
					t.Fatal(err)
				}
				release <- struct{}{}
			}
			awaitHistoricalNotice(t, settled)
			wantCalls := int32(0)
			if transition == "retry_new_input" {
				wantCalls = 1
			}
			if calls.Load() != wantCalls {
				t.Fatalf("superseded notice reached transport: calls=%d want=%d", calls.Load(), wantCalls)
			}
		})
	}
}

func TestIMHistoricalInputNoticePreservesManualCancellationAndLiveOwnerFences(t *testing.T) {
	for _, state := range []string{"manual", "cancelled", "live"} {
		t.Run(state, func(t *testing.T) {
			store, repo, db, stream := historicalIMNoticeFixture(t, state)
			var calls atomic.Int32
			app, _, _ := historicalNoticeServer(t, t.TempDir(), store, repo, stream, nil,
				func(context.Context, adaptercommon.IMSessionBinding, adaptercommon.ServerMessage) (adaptercommon.OutboundReport, error) {
					calls.Add(1)
					return adaptercommon.OutboundReport{}, nil
				})
			if _, err := app.RecoverIMOutbound(t.Context()); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE event_type='host_recovery_notice'`).Scan(&count); err != nil || count != 0 || calls.Load() != 0 {
				t.Fatalf("non-historical wait was relabeled: notices=%d calls=%d err=%v", count, calls.Load(), err)
			}
		})
	}
}
