package kernel

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestReplLibrarySaveToInboxUsesOriginalRequestWithoutIdentityOverrides(t *testing.T) {
	manager, _ := newHostCallTestSession(t, nil)
	var calls []HostCall
	policy := &HostCallPolicy{AllowedMethods: []string{"host.library.save_to_inbox"}, Handler: func(_ context.Context, call HostCall) (any, error) {
		calls = append(calls, call)
		if call.Method != "host.library.save_to_inbox" || len(call.Args) != 1 || len(call.Kwargs) != 0 {
			t.Fatalf("call=%#v", call)
		}
		return map[string]any{"schema": "x-science.literature-task-inbox-receipt.v1", "candidates": []any{map[string]any{"state": "pending"}}}, nil
	}}
	outcome := executeHostCallCell(t, manager, "library-sdk", `
import host
references = [{"provider":"crossref","external_id":"10.1234/sdk","title":"SDK metadata","metadata":{}}]
operation = "01d8b659-3d0b-4c5f-a4cc-23ac76710b59"
first = host.library.save_to_inbox(references, operation_id=operation)
second = host.library.save_to_inbox(references, operation_id=operation)
print(first["candidates"][0]["state"], second["candidates"][0]["state"])
try:
    host.library.save_to_inbox(references * 11, operation_id=operation)
except ValueError:
    print("bounded")
`, policy)
	if outcome.Err != nil || outcome.Response.Error != "" || strings.TrimSpace(outcome.Response.Stdout) != "pending pending\nbounded" {
		t.Fatalf("SDK outcome=%#v err=%v", outcome.Response, outcome.Err)
	}
	if len(calls) != 2 || !reflect.DeepEqual(calls[0].Args, calls[1].Args) {
		t.Fatalf("lost-reply retry changed body: %#v", calls)
	}
}
