package server

import "x-science/internal/agentruntime"

// Only protocol metadata belongs here, never provider text or arguments.
type sessionRunnerPresentationViolation struct{ Code string }

func (failure sessionRunnerPresentationViolation) Error() string {
	return "model response progress contract is invalid"
}

// A shared, bounded collector for complete public blocks. Forwarding state is
// retained after publication so later language conversion cannot replay a block
// or reset the per-response block limit.
type runnerLanguageProgressBlock struct {
	id, text      string
	forwarded     bool
	forwardedText string
}

type runnerLanguageProgressBlocks struct {
	open   *runnerLanguageProgressBlock
	closed []runnerLanguageProgressBlock
}

func (blocks *runnerLanguageProgressBlocks) accept(event agentruntime.ModelStreamEvent) (bool, error) {
	switch event.Kind {
	case agentruntime.ModelStreamEventPublicProgressDelta:
		if err := agentruntime.ValidateModelStreamEvent(event); err != nil {
			return true, sessionRunnerPresentationViolation{Code: "invalid_progress_event"}
		}
		if event.BlockID == "" {
			return true, sessionRunnerPresentationViolation{Code: "missing_block_identity"}
		}
		if len(blocks.closed) >= 64 {
			return true, sessionRunnerPresentationViolation{Code: "too_many_blocks"}
		}
		if blocks.open == nil {
			blocks.open = &runnerLanguageProgressBlock{id: event.BlockID}
		}
		if blocks.open.id != event.BlockID {
			return true, sessionRunnerPresentationViolation{Code: "block_identity_changed"}
		}
		if len(blocks.open.text)+len(event.ContentDelta) > 16*1024 {
			return true, sessionRunnerPresentationViolation{Code: "block_too_large"}
		}
		blocks.open.text += event.ContentDelta
		return true, nil
	case agentruntime.ModelStreamEventPublicProgressBoundary:
		if err := agentruntime.ValidateModelStreamEvent(event); err != nil {
			return true, sessionRunnerPresentationViolation{Code: "invalid_progress_event"}
		}
		if blocks.open == nil || blocks.open.id != event.BlockID {
			return true, sessionRunnerPresentationViolation{Code: "unexpected_boundary"}
		}
		blocks.closed = append(blocks.closed, *blocks.open)
		blocks.open = nil
		return true, nil
	default:
		return false, nil
	}
}
