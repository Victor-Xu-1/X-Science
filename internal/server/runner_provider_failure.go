package server

import (
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/providers"
)

const sessionRunnerMissingModelConfigurationDetail = "No active model configuration is available. Configure or select a model, then continue this same task."

// A missing configuration is proven before a request, never inferred from a
// provider response or the saved provider row's historical configuration flag.
func runnerProviderFailureKind(interruption transcriptstore.RunnerInterruption) string {
	if interruption.ReasonCode != sessionRunnerModelProviderUnavailableReasonCode {
		return ""
	}
	if interruption.ProviderFailureKind != "" {
		return interruption.ProviderFailureKind
	}
	if interruption.ResumeDetail == sessionRunnerMissingModelConfigurationDetail {
		return "configuration_missing"
	}
	if _, kind, ok := providers.ParseLegacyHTTPFailure(interruption.ResumeDetail); ok {
		return string(kind)
	}
	return "unavailable"
}

func publicRunnerProviderFailureDetail(kind, language string) string {
	switch kind {
	case "configuration_missing":
		return localizedSessionRunnerFailureMessage(language, "configuration")
	case "subscription":
		return localizedSessionRunnerFailureMessage(language, "subscription")
	case "authentication":
		return localizedSessionRunnerFailureMessage(language, "authentication")
	case "access_denied":
		return localizedSessionRunnerFailureMessage(language, "access")
	default:
		return localizedSessionRunnerFailureMessage(language, "provider")
	}
}
