package server

import transcriptstore "x-science/internal/persistence/transcript"

const sessionRunnerFinalPresentationReasonCode = "response_presentation_invalid"

// Only an unusable final candidate requires a replacement response. Optional
// progress failures with valid actions never implement this correction contract.
type sessionRunnerFinalPresentationCorrection struct{}

func (sessionRunnerFinalPresentationCorrection) Error() string {
	return "final response presentation is invalid"
}

func (sessionRunnerFinalPresentationCorrection) runnerCorrection() transcriptstore.RunnerInterruptionCause {
	return newRunnerTextCorrection(sessionRunnerFinalPresentationReasonCode,
		"replace only the final response presentation using the existing completed evidence; do not repeat completed scientific operations or publish malformed progress control fields")
}
