package server

import (
	"context"
	"encoding/json"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

func (s *Server) publicTranscriptTerminalProjection(
	ctx context.Context,
	stream transcriptstore.Stream,
	projection transcriptstore.TerminalProjection,
) transcriptstore.TerminalProjection {
	if !strings.EqualFold(strings.TrimSpace(projection.TerminalStatus), "failed") {
		return projection
	}
	projection.Detail = publicSessionRunnerFailureMessage(
		projection.Detail,
		s.transcriptWebTaskLanguage(ctx, stream, projection.Detail),
		projection.ReasonCode,
	)
	return projection
}

func (s *Server) sanitizeTranscriptWebTerminalMessages(
	ctx context.Context,
	stream transcriptstore.Stream,
	messages []map[string]any,
) {
	language := ""
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(webString(message["terminal_status"])), "failed") {
			continue
		}
		content, ok := message["content"].(map[string]any)
		if !ok {
			continue
		}
		if language == "" {
			language = s.transcriptWebTaskLanguage(ctx, stream, webString(content["content"]))
		}
		content["content"] = publicSessionRunnerFailureMessage(webString(content["content"]), language, webString(message["terminal_reason_code"]))
	}
}

// sanitizeTranscriptWebPublicMessages applies the same publication boundary
// to historical assistant text that was persisted before the current runner
// contract. It never rewrites the authoritative transcript; the copy returned
// by the Web read model is filtered so old recovery narration cannot reappear
// after refresh or conversation navigation.
func (s *Server) sanitizeTranscriptWebPublicMessages(messages []map[string]any) {
	sanitizeTranscriptWebPublicMessagesWithCodes(messages, nil)
}

// sanitizeTranscriptWebPublicMessagesForTask may preserve only machine codes
// that the user explicitly requested in the final answer and that are bound to
// the first failed durable receipt in this logical task. All other runtime
// identifiers remain behind the ordinary public-history boundary.
func (s *Server) sanitizeTranscriptWebPublicMessagesForTask(
	ctx context.Context,
	stream transcriptstore.Stream,
	messages []map[string]any,
) {
	sanitizeTranscriptWebPublicMessagesWithCodes(
		messages, s.transcriptWebExplicitFailureCodes(ctx, stream),
	)
}

func sanitizeTranscriptWebPublicMessagesWithCodes(
	messages []map[string]any,
	preservedCodes map[string]struct{},
) {
	for _, message := range messages {
		messageType := strings.ToLower(strings.TrimSpace(webString(message["type"])))
		if messageType != "text" && messageType != "assistant" && messageType != "thinking" {
			continue
		}
		sanitizeTranscriptWebPublicMessage(message, preservedCodes)
	}
}

// Rebind optional presentation only from an already valid canonical preimage.
// This response transaction never changes the stored body or source findings.
func sanitizeTranscriptWebPublicMessage(message map[string]any, preservedCodes map[string]struct{}) {
	content, object := message["content"].(map[string]any)
	if !object {
		sanitizeTranscriptWebPublicValue(message["content"], preservedCodes)
		return
	}
	value, present := content["completionAdvisories"]
	if !present {
		sanitizeTranscriptWebPublicValue(content, preservedCodes)
		return
	}
	original, text := content["content"].(string)
	attempt, identified := transcriptWebArtifactRecoveryAttempt(webString(message["conversation_id"]), message)
	var wire *completionAdvisoryEnvelope
	if text && identified {
		wire = sessionRunnerCompletionAdvisoriesFromPayload(map[string]any{"text": original, "completion_advisories": value}, attempt)
	}
	// Retain the original recursive privacy boundary even for invalid metadata.
	// In particular an unknown object must not protect nested content/text keys.
	sanitizeTranscriptWebPublicValue(content, preservedCodes)
	delete(content, "completionAdvisories")
	if wire == nil || wire.Spans == nil {
		return
	}
	projection := sessionRunnerHistoricalPublicTextProjection(original, preservedCodes)
	if projection.text != webString(content["content"]) {
		return
	}
	spans, valid := projection.advisorySpans(original, wire.Spans)
	if !valid {
		return
	}
	// Normalize pointer and read-model JSON shapes through the same recursive
	// policy. Normal Items/Findings/Sources remain byte-identical; no wholesale
	// exemption is granted to a metadata subtree if that policy needs to clean it.
	raw, err := json.Marshal(wire)
	if err != nil {
		return
	}
	var projected map[string]any
	if json.Unmarshal(raw, &projected) != nil {
		return
	}
	sanitizeTranscriptWebPublicValue(projected, preservedCodes)
	projected["spans"] = spans
	projected["text_length"] = sessionRunnerPublicTextUTF16Length(projection.text)
	projected["text_sha256"] = completionAdvisorySHA(projection.text)
	if validated := sessionRunnerCompletionAdvisoriesFromPayload(map[string]any{
		"text": projection.text, "completion_advisories": projected,
	}, attempt); validated != nil {
		content["completionAdvisories"] = validated
	}
}

func sanitizeTranscriptWebPublicValue(value any, preservedCodes map[string]struct{}) {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "content", "text", "thinking":
				if text, ok := item.(string); ok {
					typed[key] = sessionRunnerSanitizeHistoricalPublicTextWithCodes(text, preservedCodes)
					continue
				}
			}
			sanitizeTranscriptWebPublicValue(item, preservedCodes)
		}
	case []any:
		for _, item := range typed {
			sanitizeTranscriptWebPublicValue(item, preservedCodes)
		}
	}
}

func sessionRunnerSanitizeHistoricalPublicTextWithCodes(
	content string,
	preservedCodes map[string]struct{},
) string {
	return sessionRunnerHistoricalPublicTextProjection(content, preservedCodes).text
}

func sessionRunnerPublicTextExposesUntrustedInternals(
	content string,
	preservedCodes map[string]struct{},
) bool {
	if len(preservedCodes) == 0 {
		return sessionRunnerProgressNarrationExposesProductInternals(content)
	}
	masked := sessionRunnerInternalIdentifierPattern.ReplaceAllStringFunc(content, func(identifier string) string {
		if _, allowed := preservedCodes[strings.ToLower(strings.TrimSpace(identifier))]; allowed {
			return "authorized-machine-code"
		}
		return identifier
	})
	return sessionRunnerProgressNarrationExposesProductInternals(masked)
}

func (s *Server) transcriptWebExplicitFailureCodes(
	ctx context.Context,
	stream transcriptstore.Stream,
) map[string]struct{} {
	if s == nil || s.transcriptStore == nil || strings.TrimSpace(stream.UID) == "" ||
		strings.TrimSpace(stream.OwnerID) == "" {
		return nil
	}
	intent, found, err := s.transcriptStore.GetActiveFrameTaskIntent(ctx, stream.UID, stream.OwnerID)
	if err != nil || !found || !buildSessionRunnerExplicitToolContract(intent.Text).ReportFirstFailureCode {
		return nil
	}
	run := &sessionRunnerChatRun{
		TaskIntent: intent.Text, TaskIntentID: intent.ID, TaskIntentRevision: intent.Revision,
		Transcript: &transcriptRunnerAuthority{Stream: stream},
	}
	receipts, err := s.sessionRunnerDurableExplicitToolContractMessages(ctx, run)
	if err != nil {
		return nil
	}
	codes, failed := sessionRunnerFirstFailedToolCodes(receipts)
	if !failed || len(codes) == 0 {
		return nil
	}
	result := make(map[string]struct{}, min(len(codes), 8))
	for _, code := range codes {
		if len(result) >= 8 {
			break
		}
		code = strings.ToLower(strings.TrimSpace(code))
		if sessionRunnerMachineFailureCodePattern.MatchString(code) {
			result[code] = struct{}{}
		}
	}
	return result
}

func (s *Server) transcriptWebTaskLanguage(
	ctx context.Context,
	stream transcriptstore.Stream,
	fallbackText string,
) string {
	if s != nil && s.transcriptStore != nil && strings.TrimSpace(stream.UID) != "" &&
		strings.TrimSpace(stream.OwnerID) != "" {
		intent, found, err := s.transcriptStore.GetActiveFrameTaskIntent(ctx, stream.UID, stream.OwnerID)
		if err == nil && found && strings.TrimSpace(intent.Language) != "" {
			return strings.TrimSpace(intent.Language)
		}
	}
	return sessionRunnerResponseLanguage(fallbackText)
}
