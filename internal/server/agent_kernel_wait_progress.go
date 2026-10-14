package server

import "x-science/internal/agentruntime"

// Called only at the native notification executor boundary, never on arbitrary
// tool envelopes. A zero-time peek, idle queue or uncollected completed result
// is not a bounded wait for ongoing work.
func nativeNotificationWaitToolResult(input, result map[string]any) agentruntime.ToolResult {
	value := agentruntime.ToolResult{Value: result}
	timeout, err := agentKernelNotificationTimeoutDuration(input)
	if err != nil || timeout <= 0 || result["status"] != "timeout" {
		return value
	}
	pending, ok := result["pending_work"].(map[string]any)
	if !ok {
		return value
	}
	for _, key := range []string{"children", "executions"} {
		if entries, ok := pending[key].([]any); ok && len(entries) > 0 {
			value.PendingWorkWait = true
		}
	}
	for _, key := range []string{"active_compute_jobs", "pending_settlements", "pending_operations", "resource_waits", "undelivered_rows"} {
		if numberValue(pending[key]) > 0 {
			value.PendingWorkWait = true
		}
	}
	return value
}
