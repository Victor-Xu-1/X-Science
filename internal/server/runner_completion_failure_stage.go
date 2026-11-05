package server

type sessionRunnerCompletionFailureStage string

const (
	completionArtifactEvidence     sessionRunnerCompletionFailureStage = "artifact_evidence"
	completionScientificValidation sessionRunnerCompletionFailureStage = "scientific_artifact_validation"
	completionResearchValidation   sessionRunnerCompletionFailureStage = "research_artifact_validation"
	completionDeliveryValidation   sessionRunnerCompletionFailureStage = "delivery_validation"
	completionArtifactPublication  sessionRunnerCompletionFailureStage = "artifact_publication"
	completionGeneratedPlan        sessionRunnerCompletionFailureStage = "generated_plan_completion"
)

func (stage sessionRunnerCompletionFailureStage) valid() bool {
	switch stage {
	case completionArtifactEvidence, completionScientificValidation, completionResearchValidation,
		completionDeliveryValidation, completionArtifactPublication, completionGeneratedPlan:
		return true
	default:
		return false
	}
}

// Completion attribution keeps the original error available to the runner's
// existing classifiers. Only the bounded stage is additional durable data.
type sessionRunnerCompletionFailure struct {
	stage sessionRunnerCompletionFailureStage
	err   error
}

func (failure *sessionRunnerCompletionFailure) Error() string { return failure.err.Error() }
func (failure *sessionRunnerCompletionFailure) Unwrap() error { return failure.err }
