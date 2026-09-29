package server

import (
	"context"
	"log"
	"sort"
	"strings"
	"unicode"

	runtimekv "synon-go/internal/persistence/runtimekv"
	"synon-go/internal/providers"
)

type roundTokenSummary struct {
	Input      int64 `json:"input"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Output     int64 `json:"output"`
	Total      int64 `json:"total"`
}

type completedRoundSummary struct {
	Attempt           int64              `json:"attempt"`
	InputRevision     int64              `json:"input_revision"`
	CompletedAt       int64              `json:"completed_at"`
	ElapsedMS         int64              `json:"elapsed_ms"`
	CallCount         int64              `json:"call_count"`
	ReportedCallCount int64              `json:"reported_call_count"`
	UsageState        string             `json:"usage_state"`
	Tokens            *roundTokenSummary `json:"tokens"`
	Models            []string           `json:"models"`
}

// Uses durable provider receipts, never model prose, context estimates or the
// current model selection. One indexed audit scan per contributing session
// serves the whole history page, including immutable inherited rounds.
func (s *Server) completedRoundSummaries(ctx context.Context, streamUID, ownerID, sessionID string, attempts []int64) (map[int64]*completedRoundSummary, error) {
	authority, err := s.transcriptStore.CompletedRoundUsageAuthorities(ctx, streamUID, ownerID, attempts)
	if err != nil {
		return nil, err
	}
	result := map[int64]*completedRoundSummary{}
	targetsBySession := map[string]map[int64][]int64{}
	missingOrigin := map[int64]bool{}
	models := map[int64]map[string]bool{}
	for target, round := range authority {
		result[target] = &completedRoundSummary{Attempt: target, InputRevision: round.InputRevision, CompletedAt: round.CompletedAt.UnixMilli(), ElapsedMS: round.Elapsed.Milliseconds(), UsageState: "unavailable", Models: []string{}}
		models[target] = map[string]bool{}
		for _, attempt := range round.Attempts {
			session := round.AuditSessions[attempt]
			if session == "" {
				missingOrigin[target] = true
				continue // Missing ancestry is unavailable telemetry, never guessed.
			}
			if targetsBySession[session] == nil {
				targetsBySession[session] = map[int64][]int64{}
			}
			targetsBySession[session][attempt] = append(targetsBySession[session][attempt], target)
		}
	}
	if s.runtimeStore == nil || len(targetsBySession) == 0 {
		return result, nil
	}
	for auditSession, targets := range targetsBySession {
		err = s.runtimeStore.VisitPrefix(ctx, sessionRunnerModelAuditRuntimeNamespace, runtimeKeyFromSessionID(auditSession)+"-", func(entry runtimekv.Entry) error {
			record, ok := entry.Value.(map[string]any)
			if !ok || taskMetricStringValue(record, "sessionId") != auditSession {
				return nil
			}
			attempt, present, err := nonNegativeAuditInteger(record["attempt"])
			if err != nil || !present {
				return nil
			}
			for _, target := range targets[attempt] {
				// Audit writes happen before terminal publication. A late operational
				// record must not mutate already delivered billing information.
				if entry.UpdatedAt.After(authority[target].CompletedAt) {
					continue
				}
				summary := result[target]
				if summary.CallCount, err = checkedTaskMetricSum(summary.CallCount, 1); err != nil {
					return err
				}
				model := strings.TrimSpace(taskMetricStringValue(record, "model"))
				if model != "" && len(model) <= 256 && strings.IndexFunc(model, unicode.IsControl) < 0 && len(models[target]) < 256 {
					models[target][model] = true
				}
				tokens, available, err := roundTokensFromAudit(record)
				if err != nil {
					return err
				}
				if !available {
					continue
				}
				if summary.Tokens == nil {
					summary.Tokens = &roundTokenSummary{}
				}
				if err := addRoundTokens(summary.Tokens, tokens); err != nil {
					return err
				}
				summary.ReportedCallCount++
			}
			return nil
		})
		if err != nil {
			break
		}
	}
	if err != nil {
		// Telemetry is not execution authority: expose unavailable statistics,
		// preserve the answer, and retain a diagnostic rather than failing a task.
		log.Printf("round_usage_projection_unavailable session=%s: %v", sessionID, err)
		for _, summary := range result {
			summary.CallCount = 0
			summary.Tokens = nil
			summary.UsageState = "unavailable"
			summary.ReportedCallCount = 0
		}
		return result, nil
	}
	for target, summary := range result {
		for model := range models[target] {
			summary.Models = append(summary.Models, model)
		}
		sort.Strings(summary.Models)
		if summary.ReportedCallCount > 0 {
			summary.UsageState = "partial"
			if summary.ReportedCallCount == summary.CallCount && !missingOrigin[target] {
				summary.UsageState = "complete"
			}
		}
	}
	return result, nil
}

func roundTokensFromAudit(record map[string]any) (roundTokenSummary, bool, error) {
	var value roundTokenSummary
	fields := []struct {
		name   string
		target *int64
	}{{"promptTokens", &value.Input}, {"cacheReadTokens", &value.CacheRead}, {"cacheWriteTokens", &value.CacheWrite}, {"completionTokens", &value.Output}, {"totalTokens", &value.Total}}
	for _, field := range fields {
		number, _, err := nonNegativeAuditInteger(record[field.name])
		if err != nil {
			return value, false, err
		}
		*field.target = number
	}
	if value.Input == 0 && value.Output == 0 && value.Total == 0 && value.CacheRead == 0 && value.CacheWrite == 0 {
		return value, false, nil
	}
	cache, err := checkedTaskMetricSum(value.CacheRead, value.CacheWrite)
	if err != nil {
		return value, false, err
	}
	protocol := taskMetricStringValue(record, "protocol")
	if protocol != providers.ProtocolAnthropic {
		if cache > 0 && protocol != providers.ProtocolOpenAICompatible && protocol != providers.ProtocolOpenAIResponses && protocol != providers.ProtocolAzureOpenAI && protocol != providers.ProtocolAzureOpenAIResponses && protocol != providers.ProtocolGemini {
			return value, false, nil
		}
		// Chat/Responses/Gemini prompt counts already include cached input.
		if cache > value.Input {
			return value, false, nil
		}
		value.Input -= cache
	}
	if value.Total == 0 {
		for _, part := range []int64{value.Input, cache, value.Output} {
			value.Total, err = checkedTaskMetricSum(value.Total, part)
			if err != nil {
				return value, false, err
			}
		}
	}
	return value, true, nil
}

func addRoundTokens(total *roundTokenSummary, next roundTokenSummary) error {
	for _, field := range []struct {
		target *int64
		value  int64
	}{{&total.Input, next.Input}, {&total.CacheRead, next.CacheRead}, {&total.CacheWrite, next.CacheWrite}, {&total.Output, next.Output}, {&total.Total, next.Total}} {
		sum, err := checkedTaskMetricSum(*field.target, field.value)
		if err != nil {
			return err
		}
		*field.target = sum
	}
	return nil
}

func (s *Server) enrichTranscriptRoundSummaries(ctx context.Context, frameID string, messages []map[string]any) error {
	attempts := []int64{}
	for _, message := range messages {
		if attempt, ok := transcriptWebArtifactRecoveryAttempt(frameID, message); ok {
			attempts = append(attempts, attempt)
		}
	}
	if len(attempts) == 0 {
		return nil
	}
	frame, found, err := s.workspaceStore.GetFrame(frameID)
	if err != nil || !found {
		return err
	}
	owner, found, err := s.workspaceStore.ProjectOwnerIDContext(ctx, frame.ProjectID)
	if err != nil || !found {
		return err
	}
	stream, found, err := s.transcriptStore.GetFrameStreamBySession(ctx, owner, frameID)
	if err != nil || !found {
		return err
	}
	summaries, err := s.completedRoundSummaries(ctx, stream.UID, owner, frameID, attempts)
	if err != nil {
		return err
	}
	for _, message := range messages {
		if attempt, ok := transcriptWebArtifactRecoveryAttempt(frameID, message); ok && summaries[attempt] != nil {
			message["round_summary"] = summaries[attempt]
		}
	}
	return nil
}
