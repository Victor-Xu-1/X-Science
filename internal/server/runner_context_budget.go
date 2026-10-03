package server

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"synon-go/internal/agentruntime"
	eventjournal "synon-go/internal/persistence/journal"
)

// The request boundary sees runtime instructions, expanded tool schemas and
// results added after initial replay. It only requests the existing durable
// compaction transition; it never prunes messages or replays a provider call.
type sessionRunnerRequestContextBudget struct {
	mu        sync.Mutex
	threshold int
	armed     bool
	server    *Server
}

type sessionRunnerRequestContextPressureError struct {
	estimated int
	threshold int
}

func (e *sessionRunnerRequestContextPressureError) Error() string {
	return fmt.Sprintf("runner request context compaction required: estimated=%d threshold=%d", e.estimated, e.threshold)
}

func providerContextPressureFailure(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if message == "" {
		return false
	}
	for _, marker := range []string{
		"exceed max message tokens",
		"maximum context length is",
		"maximum context length exceeded",
		"context_length_exceeded",
		"context length exceeded",
		"context window exceeded",
		"input tokens exceed",
		"prompt is too long",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func newSessionRunnerRequestContextBudget(
	s *Server, options SessionRunnerChatOptions, entries []eventjournal.Entry,
	run *sessionRunnerChatRun, compacted bool,
) *sessionRunnerRequestContextBudget {
	if s == nil || s.settingsStore == nil || run == nil || run.Transcript == nil || !s.autoCompactEnabled() {
		return nil
	}
	return &sessionRunnerRequestContextBudget{
		server:    s,
		threshold: s.autoCompactTokenThreshold(runnerContextWindow(options)),
		armed:     !compacted && !runnerReplayFreshlyCompacted(entries),
	}
}

// A restart without new model/tool input must not repeat the same soft-budget
// compaction. Preparation checkpoints themselves do not constitute progress.
func runnerReplayFreshlyCompacted(entries []eventjournal.Entry) bool {
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if isCompactJournalEntry(entry) {
			return true
		}
		message := entry.Message
		switch strings.TrimSpace(stringValue(message["type"])) {
		case "message":
			role := strings.TrimSpace(stringValue(message["role"]))
			if role == "user" || role == "assistant" {
				return false
			}
		case "runner_checkpoint":
			if stringValue(message["toolCallId"]) != "" && sessionRunnerToolContinuityTerminalPhase(stringValue(message["toolPhase"])) {
				return false
			}
		}
	}
	return false
}

func (budget *sessionRunnerRequestContextBudget) beforeCall(ctx context.Context, request agentruntime.ModelRequest, capacities ...runnerContextCapacity) error {
	if budget == nil || ctx.Value(auxiliaryContextUsageKey{}) == true {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	rows, _, err := estimateRunnerRequestUsage(request)
	if err != nil {
		return err
	}
	estimated := 0
	for _, row := range rows {
		estimated += row.Tokens
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if len(capacities) > 0 && budget.server != nil {
		// Bind and evaluate under the same lock. Auxiliary calls skip this
		// entirely; no concurrent call can substitute another model's threshold.
		budget.threshold = budget.server.autoCompactTokenThreshold(capacities[0].Tokens)
	}
	if budget.threshold <= 0 {
		return nil
	}
	if estimated < budget.threshold {
		budget.armed = true
		return nil
	}
	if !budget.armed {
		// Compaction cannot shrink a required prompt, schema, current input or
		// already-bounded handoff indefinitely. Allow the rebuilt request to
		// run; only a later below-to-above transition rearms this soft budget.
		// An actual provider rejection retains its separate recovery path.
		return nil
	}
	for _, message := range request.Messages {
		if message.Role == "assistant" || message.Role == "tool" {
			budget.armed = false
			return &sessionRunnerRequestContextPressureError{estimated: estimated, threshold: budget.threshold}
		}
	}
	return nil
}
