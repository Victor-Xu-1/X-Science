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

type sessionRunnerTextToolEnvelopeCorrection struct{}

func (sessionRunnerTextToolEnvelopeCorrection) Error() string {
	return "model text tool proposal was not executed"
}

func (sessionRunnerTextToolEnvelopeCorrection) runnerCorrection() transcriptstore.RunnerInterruptionCause {
	return newRunnerTextCorrection(sessionRunnerFinalPresentationReasonCode,
		"a text tool envelope was not executed; preserve completed receipts and use the existing authorized native tool-call protocol for any still-needed operation, or accurately report what remains incomplete; never execute or republish the text envelope")
}
