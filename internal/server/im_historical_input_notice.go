package server

import (
	"context"
	"errors"
	"strings"

	adaptercommon "x-science/internal/adapters/common"
	transcriptstore "x-science/internal/persistence/transcript"
)

func historicalInputNoticeText(userText string) string {
	if sessionRunnerResponseLanguage(userText) == "zh" {
		return "输入和现有进度已保留，但无法验证这条历史输入的执行顺序，因此不能自动续跑。请发送一条新指令，从保留的上下文继续。"
	}
	return "Retained input and progress are preserved, but this older input cannot be resumed automatically with verified ordering. Send a new instruction to continue from the retained context."
}

func (s *Server) transcriptIMDeliveryMessage(ctx context.Context, claim transcriptstore.DeliveryClaim) (adaptercommon.ServerMessage, error) {
	if claim.Event.Type != transcriptstore.HistoricalInputNoticeEventType {
		return transcriptIMMessage(claim)
	}
	userText, current, err := s.transcriptStore.HistoricalInputNoticeCurrent(ctx, claim)
	if err != nil {
		return nil, err
	}
	if !current {
		return adaptercommon.ServerMessage{"type": "transcript_noop"}, nil
	}
	return adaptercommon.ServerMessage{"type": adaptercommon.RecoveryNoticeMessageType,
		"kind": transcriptstore.HistoricalInputNoticeKind, "text": historicalInputNoticeText(userText)}, nil
}

// validateIMRecoveryNotice runs after queueing and before every send attempt.
// Only an exact server-held delivery claim can authorize this host notice; an
// arbitrary adapter/model message cannot borrow its type or presentation text.
func (s *Server) validateIMRecoveryNotice(ctx context.Context, binding adaptercommon.IMSessionBinding, event adaptercommon.SessionOutboundEvent) (bool, error) {
	if s == nil || s.transcriptStore == nil || s.runtimeStore == nil || s.secretStore == nil {
		return false, errors.New("IM recovery notice authority is unavailable")
	}
	if event.SessionID != binding.SessionID || event.Message["type"] != adaptercommon.RecoveryNoticeMessageType ||
		event.Message["kind"] != transcriptstore.HistoricalInputNoticeKind {
		return false, transcriptstore.ErrEventConflict
	}
	claim, found := s.transcriptIMClaim(transcriptIMClaimKey{sessionID: event.SessionID, eventID: event.EventID}, event.ReceiptToken)
	if !found || claim.Event.Type != transcriptstore.HistoricalInputNoticeEventType || claim.OwnerID != binding.OwnerUserID || claim.Destination != binding.SessionID {
		return false, transcriptstore.ErrDeliveryClaimStale
	}
	if err := s.AuthorizeIMOutbound(binding); err != nil {
		return false, err
	}
	entry, found, err := s.runtimeStore.Get(imOutboundRouteNamespace, imOutboundStateKey(binding.SessionID))
	if err != nil {
		return false, err
	}
	if !found {
		// Absence of this projection is not proof of canonical revocation.
		return false, errors.New("IM recovery notice route cannot be verified")
	}
	var state imOutboundRouteState
	if err := decodeRuntimeValue(entry.Value, &state); err != nil {
		return false, err
	}
	currentBinding, warning, err := s.restoreIMOutboundBinding(state)
	if err != nil {
		return false, err
	}
	if warning != "" {
		// Unavailable encrypted context is retryable uncertainty, not evidence
		// that this notice was superseded. Do not ACK it or expose raw warning.
		return false, errors.New("IM recovery notice context is unavailable")
	}
	// Bind applies these two defaults. Other persisted fields were normalized
	// when written; include the recipient and encrypted context, not just owner.
	currentBinding.TargetType = firstNonEmpty(strings.ToLower(strings.TrimSpace(currentBinding.TargetType)), "chat")
	currentBinding.TargetID = firstNonEmpty(strings.TrimSpace(currentBinding.TargetID), currentBinding.ChatID)
	if currentBinding != binding {
		return false, nil
	}
	userText, current, err := s.transcriptStore.HistoricalInputNoticeCurrent(ctx, claim)
	if err != nil || !current {
		return false, err
	}
	if event.Message["text"] != historicalInputNoticeText(userText) {
		return false, transcriptstore.ErrEventConflict
	}
	return true, nil
}
