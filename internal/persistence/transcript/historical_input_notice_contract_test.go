package transcript

import (
	"errors"
	"testing"
	"time"
)

func TestHistoricalNoticeTypeCannotBeWrittenByGenericRunner(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	seedTranscriptInput(t, repo, "reserved-host-notice", "owner")
	claim, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: "reserved-host-notice", OwnerID: "owner",
		RunnerID: "owned-runner", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
	if err != nil || !claim.Claimed {
		t.Fatalf("actual claim=%+v err=%v", claim, err)
	}
	_, created, err := repo.AppendRunnerEvent(t.Context(), AppendEventInput{Claim: claim.Claim, ClientMessageID: "attempted-host-notice",
		Type: HistoricalInputNoticeEventType, Source: EventSourcePayload, PayloadJSON: []byte(`{"kind":"historical_input_order_unknown"}`)})
	if !errors.Is(err, ErrReservedEventType) || created {
		t.Errorf("generic runner wrote a host-only presentation event: created=%t err=%v", created, err)
	}
	var notices int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE event_type=?`, HistoricalInputNoticeEventType).Scan(&notices); err != nil || notices != 0 {
		t.Fatalf("host event must remain absent: count=%d err=%v", notices, err)
	}
}

func TestHistoricalNoticeUnreadableLanguageMetadataPreservesValidUserSource(t *testing.T) {
	for _, test := range []struct{ name, payload, want string }{
		{"nonstring_text", `{"text":123,"attachment":"retained-input"}`, ""},
		{"missing_text", `{"attachment":"retained-input"}`, ""},
		{"actual_chinese_user_text", `{"text":"继续已有任务"}`, "继续已有任务"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, db, _ := newTranscriptRepository(t)
			stream, err := repo.CreateStream(t.Context(), CreateStreamInput{UID: "notice-language", OwnerID: "owner",
				ExternalID: "notice-language", Kind: StreamKindStandalone, Epoch: 1})
			if err != nil {
				t.Fatal(err)
			}
			event, created, err := repo.AppendUserEvent(t.Context(), AppendUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
				ClientMessageID: "actual-user-source", PayloadJSON: []byte(test.payload)})
			if err != nil || !created {
				t.Fatalf("real user writer did not accept valid source: created=%t err=%v", created, err)
			}
			branch, err := repo.GetBranchState(t.Context(), stream.UID, stream.OwnerID)
			if err != nil {
				t.Fatal(err)
			}
			conn, err := db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			state := historicalInputNoticeState{Stream: stream.UID, Branch: branch.ActiveBranchID}
			text, err := historicalNoticeUserSourceConn(t.Context(), conn, &state)
			if err != nil || text != test.want || state.UserEvent != event.EventID || state.UserSHA256 == "" {
				t.Errorf("optional language changed source authority: text=%q state=%+v err=%v", text, state, err)
			}
			var unchanged string
			if err := conn.QueryRowContext(t.Context(), `SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`, stream.UID, event.EventID).Scan(&unchanged); err != nil || unchanged != test.payload {
				t.Fatalf("language projection changed private source: %q err=%v", unchanged, err)
			}
		})
	}
}
