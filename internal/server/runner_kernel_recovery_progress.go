package server

import (
	"context"
	"encoding/json"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/toolprogress"
)

// Recovery bypasses Engine execution, but must retain its progress observation
// contract. Only progress is emitted here: the caller still owns the original
// start and exact durable terminal receipt. Arguments remain the immutable
// batch proposal even when admission normalized the execution input.
func observeRecoveredKernelTool(
	ctx context.Context, call agentruntime.ToolCall, arguments json.RawMessage,
	execute func(context.Context) (map[string]any, error), publish func(agentruntime.Event) error,
) (map[string]any, error) {
	immutableArguments := string(arguments)
	return toolprogress.Observe(ctx, 0, execute, func(progress *toolprogress.Update, elapsed time.Duration, ordinal int) {
		// The existing publication path namespaces progress by live claim and
		// original tool identity. Telemetry failure never cancels execution.
		_ = publish(agentruntime.Event{
			Type: agentruntime.EventToolProgress, ToolName: call.Name, ToolCallID: call.ID,
			Arguments: immutableArguments, Elapsed: elapsed, ProgressOrdinal: ordinal, Progress: progress,
		})
	})
}
