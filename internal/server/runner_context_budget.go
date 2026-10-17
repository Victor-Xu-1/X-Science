package server

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
)

// The request boundary sees runtime instructions, expanded tool schemas and
// results added after initial replay. It only requests the existing durable
// compaction transition; it never prunes messages or replays a provider call.
type sessionRunnerRequestContextBudget struct {
	mu      sync.Mutex
	episode *runnerCompactionEpisode
	server  *Server
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
	run *sessionRunnerChatRun, episode *runnerCompactionEpisode,
) *sessionRunnerRequestContextBudget {
	if s == nil || s.settingsStore == nil || run == nil || run.Transcript == nil || !s.autoCompactEnabled() {
		return nil
	}
	if episode == nil {
		binding := runnerPreparationCompactionModel(options).binding(s.autoCompactTokenThreshold(runnerContextWindow(options)))
		episode = restoreRunnerCompactionEpisode(binding, entries)
	}
	return &sessionRunnerRequestContextBudget{server: s, episode: episode}
}

func (budget *sessionRunnerRequestContextBudget) noteDurableProgress(ctx context.Context) {
	if budget == nil || ctx.Err() != nil || ctx.Value(auxiliaryContextUsageKey{}) == true ||
		budget.server != nil && !budget.server.autoCompactEnabled() {
		return
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	budget.episode.armed = true
}

func (budget *sessionRunnerRequestContextBudget) beforeCall(ctx context.Context, request agentruntime.ModelRequest, models ...runnerCompactionModel) error {
	if budget == nil || ctx.Value(auxiliaryContextUsageKey{}) == true {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if budget.server != nil && !budget.server.autoCompactEnabled() {
		return nil
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
	if len(models) > 0 {
		// Bind and evaluate under the same lock. Auxiliary calls skip this
		// entirely; no concurrent call can substitute another model's threshold.
		threshold := budget.episode.binding.ThresholdTokens
		if budget.server != nil {
			threshold = budget.server.autoCompactTokenThreshold(models[0].capacity.Tokens)
		}
		budget.episode.bind(models[0].binding(threshold))
	}
	threshold := budget.episode.binding.ThresholdTokens
	if threshold <= 0 {
		return nil
	}
	if estimated < threshold {
		budget.episode.armed = true
		return nil
	}
	if !budget.episode.armed {
		// Compaction cannot shrink a required prompt, schema, current input or
		// already-bounded handoff indefinitely. Allow the rebuilt request to
		// run until a new binding, durable outcome or below-to-above transition
		// rearms this soft budget.
		// An actual provider rejection retains its separate recovery path.
		return nil
	}
	for _, message := range request.Messages {
		if message.Role == "assistant" || message.Role == "tool" {
			budget.episode.armed = false
			return &sessionRunnerRequestContextPressureError{estimated: estimated, threshold: threshold}
		}
	}
	return nil
}
