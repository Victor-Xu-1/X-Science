package workspace

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFrameResumeWakeAuditModelSwitchPreservesVerifiedWaitOrigin(t *testing.T) {
	for _, test := range []struct {
		name   string
		legacy bool
		origin string
	}{
		{name: "native", origin: "verified"},
		{name: "legacy-verified", legacy: true, origin: "verified"},
		{name: "legacy-missing", legacy: true, origin: "missing"},
		{name: "legacy-mismatch", legacy: true, origin: "mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newResumeWakeAuditStore(t)
			wait := parkResumeWakeAudit(t, store)
			originID := wait.WaitRef
			if test.legacy {
				if _, err := store.db.Exec(`UPDATE frame_events SET payload=json_remove(payload,'$.dispatch.waitEventId') WHERE id=?`, wait.ResumeEvent.ID); err != nil {
					t.Fatal(err)
				}
			}
			switch test.origin {
			case "missing":
				if _, err := store.db.Exec(`DELETE FROM frame_events WHERE id=?`, originID); err != nil {
					t.Fatal(err)
				}
			case "mismatch":
				if _, err := store.db.Exec(`UPDATE frame_events SET payload=json_set(payload,'$.checkpointEventId',999) WHERE id=?`, originID); err != nil {
					t.Fatal(err)
				}
			}
			current, found, err := store.GetCompatibilityFrameResumeDispatch(wait.ResumeEvent.ID)
			if err != nil || !found {
				t.Fatalf("wait=%#v found=%t err=%v", current, found, err)
			}
			if test.legacy && !strings.HasPrefix(current.WaitRef, "legacy-wait:") {
				t.Fatalf("legacy reference=%q", current.WaitRef)
			}
			event, state, err := store.SignalCompatibilityFrameResumeDispatchModelSwitch(wait.FrameID, 7, "provider_failure")
			if err != nil || state != CompatibilityFrameResumeDispatchWakeWoken {
				t.Fatalf("wake=%#v state=%s err=%v", event, state, err)
			}
			var raw string
			if err := store.db.QueryRow(`SELECT payload FROM frame_events WHERE id=?`, event.ID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var audit struct {
				WaitRef           string               `json:"waitRef"`
				WaitOriginEventID string               `json:"waitOriginEventId"`
				Source            string               `json:"source"`
				Cause             FrameResumeWakeCause `json:"cause"`
			}
			if err := json.Unmarshal([]byte(raw), &audit); err != nil {
				t.Fatal(err)
			}
			wantOrigin := ""
			if test.origin == "verified" {
				wantOrigin = originID
			}
			if audit.WaitRef != current.WaitRef || audit.WaitOriginEventID != wantOrigin {
				t.Fatalf("audit=%#v want wait=%q origin=%q", audit, current.WaitRef, wantOrigin)
			}
			if audit.Source != string(FrameResumeWakeModelSwitch) || audit.Cause.Kind != "model_selection_revision" || audit.Cause.ReferenceID != wait.FrameID || audit.Cause.Revision != 7 || audit.Cause.Evidence != "observed" {
				t.Fatalf("model cause changed: %#v", audit)
			}
			if _, state, err := store.SignalCompatibilityFrameResumeDispatchModelSwitch(wait.FrameID, 8, "provider_failure"); err != nil || state != CompatibilityFrameResumeDispatchWakePending {
				t.Fatalf("duplicate state=%s err=%v", state, err)
			}
			var count int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE event_type='frame_resume_dispatch_woken'`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("wake count=%d err=%v", count, err)
			}
			var after string
			if err := store.db.QueryRow(`SELECT payload FROM frame_events WHERE id=?`, event.ID).Scan(&after); err != nil || after != raw {
				t.Fatalf("duplicate replaced audit: before=%s after=%s err=%v", raw, after, err)
			}
		})
	}
}
