package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	adaptercommon "x-science/internal/adapters/common"
)

func TestIMHistoricalNoticeLocalizesOnlyFixedCopyFromLatestUserSource(t *testing.T) {
	for _, test := range []struct{ name, userText, want string }{
		{"english_default", "ORIGINAL_USER_SENTINEL: continue the retained task", historicalNoticeEnglish},
		{"latest_chinese_user", "请继续已有任务。ORIGINAL_USER_SENTINEL", "输入和现有进度已保留，但无法验证这条历史输入的执行顺序，因此不能自动续跑。请发送一条新指令，从保留的上下文继续。"},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload, err := json.Marshal(map[string]string{"text": test.userText, "private_error": "PRIVATE_DIAGNOSTIC_SENTINEL"})
			if err != nil {
				t.Fatal(err)
			}
			store, repo, db, stream := historicalIMNoticeFixture(t, "completed", payload)
			var calls atomic.Int32
			app, dispatcher, settled := historicalNoticeServer(t, t.TempDir(), store, repo, stream, nil,
				func(_ context.Context, _ adaptercommon.IMSessionBinding, message adaptercommon.ServerMessage) (adaptercommon.OutboundReport, error) {
					encoded, err := json.Marshal(message)
					if err != nil || message["type"] != "content_delta" || message["text"] != test.want ||
						strings.Contains(string(encoded), "ORIGINAL_USER_SENTINEL") || strings.Contains(string(encoded), "PRIVATE_DIAGNOSTIC_SENTINEL") ||
						strings.Contains(string(encoded), "private-test-context") {
						t.Errorf("fixed localized notice leaked/replaced private source: message=%s err=%v", encoded, err)
					}
					calls.Add(1)
					return adaptercommon.OutboundReport{Platform: "wechat"}, nil
				})
			if _, err := app.RecoverIMOutbound(t.Context()); err != nil {
				t.Fatal(err)
			}
			awaitHistoricalNotice(t, settled)
			var identity string
			if err := db.QueryRow(`SELECT client_message_id FROM transcript_events WHERE stream_uid=? AND event_type='host_recovery_notice'`, stream.UID).Scan(&identity); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := app.RecoverIMOutbound(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			closeTranscriptIMTestDispatcher(t, dispatcher)
			var notices, inputs, attempts int
			var sameIdentity, preservedUserPayload string
			if err := db.QueryRow(`SELECT COUNT(*),client_message_id FROM transcript_events WHERE stream_uid=? AND event_type='host_recovery_notice'`, stream.UID).Scan(&notices, &sameIdentity); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT payload_json FROM transcript_events WHERE stream_uid=? AND client_message_id='queued'`, stream.UID).Scan(&preservedUserPayload); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM transcript_events WHERE event_type IN ('user_message','user_input_response')),
				(SELECT COUNT(*) FROM transcript_runner_attempts)`).Scan(&inputs, &attempts); err != nil {
				t.Fatal(err)
			}
			current, err := repo.GetStream(t.Context(), stream.UID, stream.OwnerID)
			if err != nil || calls.Load() != 1 || notices != 1 || sameIdentity != identity || preservedUserPayload != string(payload) || inputs != 2 || attempts != 1 ||
				current.InputRevision != stream.InputRevision || current.ConsumedInputRevision != stream.ConsumedInputRevision {
				t.Fatalf("localization changed authority or logical notice identity: calls=%d notices=%d inputs=%d attempts=%d stream=%+v err=%v",
					calls.Load(), notices, inputs, attempts, current, err)
			}
		})
	}
}
