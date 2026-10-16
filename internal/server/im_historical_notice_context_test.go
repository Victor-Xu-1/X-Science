package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	adaptercommon "x-science/internal/adapters/common"
)

func TestIMHistoricalNoticeUnverifiedContextRetainsItsRetryReceipt(t *testing.T) {
	for _, unavailable := range []string{"encrypted_context", "route_record"} {
		t.Run(unavailable, func(t *testing.T) {
			store, repo, db, stream := historicalIMNoticeFixture(t, "completed")
			ready, release := make(chan struct{}), make(chan struct{})
			var first, unblock sync.Once
			releaseWorker := func() { unblock.Do(func() { close(release) }) }
			defer releaseWorker()
			var calls atomic.Int32
			app, dispatcher, settled := historicalNoticeServer(t, t.TempDir(), store, repo, stream,
				func(adaptercommon.IMSessionBinding) error {
					first.Do(func() {
						close(ready)
						select {
						case <-release:
						case <-t.Context().Done():
						}
					})
					return t.Context().Err()
				},
				func(_ context.Context, _ adaptercommon.IMSessionBinding, message adaptercommon.ServerMessage) (adaptercommon.OutboundReport, error) {
					if message["type"] != "content_delta" || message["text"] != historicalNoticeEnglish {
						t.Errorf("unexpected transport projection: %#v", message)
					}
					calls.Add(1)
					return adaptercommon.OutboundReport{Platform: "wechat"}, nil
				})
			if _, err := app.RecoverIMOutbound(t.Context()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-ready:
			case <-time.After(time.Second):
				t.Fatal("notice did not reach the actual queued consumer")
			}
			var eventID, publication int64
			var identity, payload string
			if err := db.QueryRow(`SELECT event_id,publication_seq,client_message_id,payload_json FROM transcript_events
				WHERE stream_uid=? AND event_type='host_recovery_notice'`, stream.UID).Scan(&eventID, &publication, &identity, &payload); err != nil {
				t.Fatal(err)
			}
			route, found, err := repo.GetDeliveryRoute(t.Context(), stream.OwnerID, stream.UID, stream.SessionID)
			if err != nil || !found || route.Status != "active" {
				t.Fatalf("canonical route unavailable before controlled change: %+v found=%t err=%v", route, found, err)
			}
			var restore func() error
			if unavailable == "encrypted_context" {
				secretID := internalIMRouteSecretID(stream.SessionID, stream.OwnerID)
				if deleted, err := app.secretStore.DeleteForUser(secretID, stream.OwnerID); err != nil || !deleted {
					t.Fatalf("remove private test context: deleted=%t err=%v", deleted, err)
				}
				restore = func() error { return app.upsertInternalIMRouteSecret(secretID, stream.OwnerID, "private-test-context") }
			} else {
				key := imOutboundStateKey(stream.SessionID)
				entry, found, err := app.runtimeStore.Get(imOutboundRouteNamespace, key)
				if err != nil || !found {
					t.Fatalf("private route fixture unavailable: found=%t err=%v", found, err)
				}
				if deleted, err := app.runtimeStore.Delete(imOutboundRouteNamespace, key); err != nil || !deleted {
					t.Fatalf("remove private route projection: deleted=%t err=%v", deleted, err)
				}
				restore = func() error { _, err := app.runtimeStore.Set(imOutboundRouteNamespace, key, entry.Value); return err }
			}
			releaseWorker()
			select {
			case err := <-settled:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("queued outcome was not durably settled")
			}
			intent, err := repo.GetDeliveryIntent(t.Context(), stream.OwnerID, stream.UID, publication, stream.SessionID, route.Generation)
			if err != nil || intent.Status != "pending" || intent.DeliveredAt != nil || intent.LastErrorCode != "im_delivery_failed" || calls.Load() != 0 {
				t.Errorf("unverified context was ACKed as superseded: status=%s delivered=%t code=%s sends=%d err=%v",
					intent.Status, intent.DeliveredAt != nil, intent.LastErrorCode, calls.Load(), err)
			}
			if err := restore(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, err := app.RecoverIMOutbound(t.Context()); err != nil {
					t.Fatal(err)
				}
				intent, err = repo.GetDeliveryIntent(t.Context(), stream.OwnerID, stream.UID, publication, stream.SessionID, route.Generation)
				if err != nil {
					t.Fatal(err)
				}
				if intent.Status == "delivered" || time.Now().After(deadline) {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			closeTranscriptIMTestDispatcher(t, dispatcher)
			var notices int
			var preservedID, preservedPayload string
			if err := db.QueryRow(`SELECT COUNT(*),client_message_id,payload_json FROM transcript_events
				WHERE stream_uid=? AND event_type='host_recovery_notice' AND event_id=? AND publication_seq=?`,
				stream.UID, eventID, publication).Scan(&notices, &preservedID, &preservedPayload); err != nil {
				t.Fatal(err)
			}
			current, err := repo.GetStream(t.Context(), stream.UID, stream.OwnerID)
			if err != nil || intent.Status != "delivered" || intent.AttemptCount != 2 || calls.Load() != 1 || notices != 1 ||
				preservedID != identity || preservedPayload != payload || current.InputRevision != stream.InputRevision || current.ConsumedInputRevision != stream.ConsumedInputRevision {
				t.Fatalf("restored route lost/duplicated its original intent: status=%s attempts=%d sends=%d notices=%d stream=%+v err=%v",
					intent.Status, intent.AttemptCount, calls.Load(), notices, current, err)
			}
		})
	}
}
