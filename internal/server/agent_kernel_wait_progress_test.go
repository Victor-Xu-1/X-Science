package server

import "testing"

func TestNativeNotificationWaitProofRequiresBoundedPendingWork(t *testing.T) {
	for _, test := range []struct {
		name, status string
		seconds      float64
		pending      map[string]any
		want         bool
	}{
		{"compute", "timeout", 30, map[string]any{"executions": []any{map[string]any{"exec_id": "owned"}}}, true},
		{"child", "timeout", 30, map[string]any{"children": []any{map[string]any{"frame_id": "child"}}}, true},
		{"resource", "timeout", 30, map[string]any{"resource_waits": 1}, true},
		{"dependency", "timeout", 30, map[string]any{"pending_operations": 1}, true},
		{"settlement", "timeout", 30, map[string]any{"pending_settlements": 1}, true},
		{"remote", "timeout", 30, map[string]any{"active_compute_jobs": 1}, true},
		{"idle", "idle", 30, map[string]any{}, false},
		{"peek", "timeout", 0, map[string]any{"resource_waits": 1}, false},
		{"no work", "timeout", 30, map[string]any{}, false},
		{"completed unread", "timeout", 30, map[string]any{"unread_notifications": 1}, false},
		{"uncollected", "uncollected_results", 30, map[string]any{"uncollected_landings": []any{"done"}}, false},
		{"received", "received", 30, map[string]any{"resource_waits": 1}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			native := map[string]any{"status": test.status, "num_notifications": 0, "pending_work": test.pending}
			result := nativeNotificationWaitToolResult(map[string]any{"timeout_seconds": test.seconds}, native)
			if result.PendingWorkWait != test.want {
				t.Fatalf("pending proof=%t want=%t", result.PendingWorkWait, test.want)
			}
			if runnerToolCompletionHasMaterialProgress("wait_for_notification", native) {
				t.Fatal("pending wait falsely reset material progress")
			}
		})
	}
}
