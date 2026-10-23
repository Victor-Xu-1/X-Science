package server

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

func TestHostGrantSnapshotBindsOwnerRowsAndRecordedChanges(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	a := hostGrant{ID: "/fixture/a", Path: "/fixture/a", Mode: "read", CreatedAt: now, UpdatedAt: now}
	b := hostGrant{ID: "/fixture/b", Path: "/fixture/b", Mode: "read_write", CreatedAt: now, UpdatedAt: now}
	input := []hostGrant{b, a}
	before := append([]hostGrant(nil), input...)
	first, err := hostGrantSnapshotResult("owner", input)
	if err != nil || !validHostGrantSnapshotRevision(stringValue(first["snapshotRevision"])) {
		t.Fatalf("snapshot=%v err=%v", first, err)
	}
	reordered, err := hostGrantSnapshotResult("owner", []hostGrant{a, b})
	if err != nil || !reflect.DeepEqual(first, reordered) || !reflect.DeepEqual(input, before) {
		t.Fatal("snapshot is order-dependent or mutates the grant source")
	}
	rows := first["grants"].([]any)
	if rows[0].(map[string]any)["path"] != a.Path || rows[0].(map[string]any)["mode"] != "ro" ||
		rows[1].(map[string]any)["mode"] != "rw" || rows[0].(map[string]any)["grantedAt"] != now.Format(time.RFC3339Nano) {
		t.Fatalf("discovery shape=%v", rows)
	}
	for _, change := range []string{"owner", "mode", "regrant", "empty"} {
		t.Run(change, func(t *testing.T) {
			owner, grants := "owner", []hostGrant{a, b}
			switch change {
			case "owner":
				owner = "another-owner"
			case "mode":
				grants[0].Mode = "read_write"
			case "regrant":
				grants[0].UpdatedAt = now.Add(time.Second)
			case "empty":
				grants = nil
			}
			changed, err := hostGrantSnapshotResult(owner, grants)
			if err != nil || changed["snapshotRevision"] == first["snapshotRevision"] {
				t.Fatalf("%s did not invalidate the exact discovery snapshot: %v %v", change, changed, err)
			}
		})
	}
	empty, err := hostGrantSnapshotResult("owner", nil)
	if err != nil || len(empty["grants"].([]any)) != 0 || !validHostGrantSnapshotRevision(stringValue(empty["snapshotRevision"])) {
		t.Fatal("a successful empty list was treated as no discovery")
	}
	legacy, err := hostGrantSnapshotResult("owner", []hostGrant{{ID: a.ID, Path: a.Path, Mode: "read"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, invented := legacy["grants"].([]any)[0].(map[string]any)["grantedAt"]; invented {
		t.Fatal("a missing historical grant time was rendered as a recorded timestamp")
	}
}

func TestHostGrantDiscoveryRequiresCompleteCanonicalResultInModelRequest(t *testing.T) {
	snapshot, err := hostGrantSnapshotResult("owner", []hostGrant{{ID: "/fixture/a", Path: "/fixture/a", Mode: "read"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(snapshot)
	var expected map[string]any
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"exact", "missing", "different-call", "user-copy", "truncated", "wrong-revision", "failed"} {
		t.Run(change, func(t *testing.T) {
			message := agentruntime.Message{Role: "tool", ToolCallID: "discovery", Content: string(raw)}
			switch change {
			case "different-call":
				message.ToolCallID = "another-call"
			case "user-copy":
				message.Role = "user"
			case "truncated", "wrong-revision", "failed":
				var modified map[string]any
				_ = json.Unmarshal(raw, &modified)
				if change == "truncated" {
					modified["grants"] = []any{}
				} else if change == "failed" {
					modified["isError"] = true
				} else {
					modified["snapshotRevision"] = "sha256:old"
				}
				encoded, _ := json.Marshal(modified)
				message.Content = string(encoded)
			}
			request := agentruntime.ModelRequest{Messages: []agentruntime.Message{message}}
			if change == "missing" {
				request.Messages = nil
			}
			if found := hostGrantDiscoveryInRequest(request, "discovery", expected); found != (change == "exact") {
				t.Fatalf("complete canonical discovery presence=%v for %s", found, change)
			}
		})
	}
	for _, invalid := range []string{"", "sha256:old", "md5:" + stringValue(snapshot["snapshotRevision"]), "sha256:" + string(make([]byte, 64))} {
		if validHostGrantSnapshotRevision(invalid) {
			t.Fatalf("malformed revision accepted: %q", invalid)
		}
	}
}
