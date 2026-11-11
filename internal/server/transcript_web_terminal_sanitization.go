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
	if projection.RetainsFinalResponse && projection.UnvalidatedFinalResponse {
		projection.Detail = publicSessionRunnerRetainedEvidenceFailureMessage(s.transcriptWebTaskLanguage(ctx, stream, projection.Detail), projection.ReasonCode)
	}
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
		if message["terminal_final_response_unvalidated"] == true {
			content["content"] = publicSessionRunnerRetainedEvidenceFailureMessage(language, webString(message["terminal_reason_code"]))
		}
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

// Page provenance is request-local and comes from immutable source events.
// Message IDs, status labels and model-authored fields never supply this proof.
func (s *Server) sanitizeTranscriptWebPublicPage(ctx context.Context, stream transcriptstore.Stream, snapshot transcriptstore.ProjectionSnapshot, records []transcriptstore.TranscriptWebMessageRecord, messages []map[string]any) {
	preserved := s.transcriptWebExplicitFailureCodes(ctx, stream)
	users := s.transcriptWebPublicUserSources(ctx, stream, snapshot, records, messages)
	canonical := map[int]string{}
	if len(records) == len(messages) && s.transcriptStore != nil {
		ranges := make([]transcriptstore.ProjectedAssistantSourceRange, len(records))
		for index, record := range records {
			ranges[index] = transcriptstore.ProjectedAssistantSourceRange{FirstPublicationSequence: record.FirstPublicationSequence, LastPublicationSequence: record.LastPublicationSequence}
		}
		sources, err := s.transcriptStore.ListProjectedAssistantSources(ctx, stream.OwnerID, snapshot, ranges)
		if err == nil {
			for _, source := range sources {
				index := source.RecordIndex
				if index < 0 || index >= len(messages) || source.Assistant.RunnerAttempt == nil {
					continue
				}
				message, record := messages[index], records[index]
				content := mapValue(message["content"])
				body, text := content["content"].(string)
				payload, decodeErr := transcriptPayloadObject(source.Assistant.PayloadJSON)
				identity, identityErr := transcriptAssistantMessageIDFromPayload(payload, stream.SessionID, *source.Assistant.RunnerAttempt)
				canonicalStatus := message["terminal_status"] == "completed"
				if source.RetainedAfterFailure {
					canonicalStatus = message["status"] == "finish" && message["terminal_status"] == nil
				}
				if decodeErr != nil || identityErr != nil || identity == "" || !text || transcriptWebAssistantMessageType(payload) != "text" ||
					message["type"] != "text" || message["position"] != "left" || !canonicalStatus || message["terminal_superseded"] == true ||
					record.MessageID != identity || record.ClientMessageID != identity || message["id"] != identity || message["msg_id"] != identity ||
					source.Assistant.StreamUID != stream.UID || source.Assistant.PublicationSeq < record.FirstPublicationSequence ||
					source.Assistant.PublicationSeq > record.LastPublicationSequence || source.Terminal.PublicationSeq != record.LastPublicationSequence ||
					completionAdvisorySHA(body) != completionAdvisorySHA(transcriptPayloadText(payload)) {
					continue
				}
				if provided, present := content["completionAdvisories"]; present {
					actual := sessionRunnerCompletionAdvisoriesFromPayload(payload, *source.Assistant.RunnerAttempt)
					materialized := sessionRunnerCompletionAdvisoriesFromPayload(map[string]any{"text": body, "completion_advisories": provided}, *source.Assistant.RunnerAttempt)
					left, _ := json.Marshal(actual)
					right, _ := json.Marshal(materialized)
					if actual == nil || materialized == nil || string(left) != string(right) {
						delete(content, "completionAdvisories")
					}
				}
				canonical[index] = completionAdvisorySHA(body)
			}
		}
	}
	for index, message := range messages {
		kind := strings.ToLower(strings.TrimSpace(webString(message["type"])))
		if kind != "text" && kind != "assistant" && kind != "thinking" {
			continue
		}
		if sanitizeTranscriptWebPublicUserMessage(message, preserved, users[index]) {
			continue
		}
		digest, proved := canonical[index]
		proved = proved && completionAdvisorySHA(webString(mapValue(message["content"])["content"])) == digest
		sanitizeTranscriptWebPublicMessagePolicy(message, preserved, proved)
	}
}

// The canonical body keeps ordinary JSON/Markdown bytes. Private/control
// source selection remains separate from content and never certifies claims.
func sessionRunnerCanonicalPublicTextProjection(content string) sessionRunnerPublicTextProjection {
	return sessionRunnerPrivateThinkProjection(content).withoutCompletionObservationReferences()
}

func sanitizeTranscriptWebPublicMessagesWithCodes(
	messages []map[string]any,
	preservedCodes map[string]struct{},
) {
	sanitizeTranscriptWebPublicMessagesWithUserSources(messages, preservedCodes, nil)
}

func sanitizeTranscriptWebPublicMessagesWithUserSources(messages []map[string]any, preservedCodes map[string]struct{}, users map[int]string) {
	for index, message := range messages {
		messageType := strings.ToLower(strings.TrimSpace(webString(message["type"])))
		if messageType != "text" && messageType != "assistant" && messageType != "thinking" {
			continue
		}
		if sanitizeTranscriptWebPublicUserMessage(message, preservedCodes, users[index]) {
			continue
		}
		sanitizeTranscriptWebPublicMessage(message, preservedCodes)
	}
}

// Rebind optional presentation only from an already valid canonical preimage.
// This response transaction never changes the stored body or source findings.
func sanitizeTranscriptWebPublicMessage(message map[string]any, preservedCodes map[string]struct{}) {
	sanitizeTranscriptWebPublicMessagePolicy(message, preservedCodes, false)
}

func sanitizeTranscriptWebPublicMessagePolicy(message map[string]any, preservedCodes map[string]struct{}, canonical bool) {
	content, object := message["content"].(map[string]any)
	if !object {
		sanitizeTranscriptWebPublicValue(message["content"], preservedCodes)
		return
	}
	value, present := content["completionAdvisories"]
	if !present && !canonical {
		sanitizeTranscriptWebPublicValue(content, preservedCodes)
		return
	}
	original, text := content["content"].(string)
	attempt, identified := transcriptWebArtifactRecoveryAttempt(webString(message["conversation_id"]), message)
	var wire *completionAdvisoryEnvelope
	if text && identified {
		wire = sessionRunnerCompletionAdvisoriesFromPayload(map[string]any{"text": original, "completion_advisories": value}, attempt)
	}
	var projection sessionRunnerPublicTextProjection
	if canonical && text {
		projection = sessionRunnerCanonicalPublicTextProjection(original)
	} else {
		projection = sessionRunnerHistoricalPublicTextProjection(original, preservedCodes)
	}
	// Retain the original recursive privacy boundary even for invalid metadata.
	// In particular an unknown object must not protect nested content/text keys.
	if canonical && text {
		metadata := make(map[string]any, len(content)-1)
		for key, item := range content {
			if key != "content" {
				metadata[key] = item
			}
		}
		sanitizeTranscriptWebPublicValue(metadata, preservedCodes)
		for key, item := range metadata {
			content[key] = item
		}
		content["content"] = projection.text
	} else {
		sanitizeTranscriptWebPublicValue(content, preservedCodes)
	}
	delete(content, "completionAdvisories")
	if wire == nil || wire.Spans == nil {
		return
	}
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
