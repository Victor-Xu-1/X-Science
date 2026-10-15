package server

import (
	"strings"

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
	if transcriptstore.ValidRunnerProviderFailureKind(interruption.ProviderFailureKind) {
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

// Canonical and legacy host checkpoints use different field spelling, but
// public consumers share one closed presentation classifier, never raw prose.
func runnerProviderFailureKindFromPayload(payload map[string]any) string {
	reason := strings.TrimSpace(firstNonEmpty(webString(payload["reason_code"]), webString(payload["reasonCode"])))
	return runnerProviderFailureKind(transcriptstore.RunnerInterruption{
		ReasonCode:          strings.ToLower(reason),
		ResumeDetail:        firstNonEmpty(webString(payload["resume_detail"]), webString(payload["resumeDetail"])),
		ProviderFailureKind: webString(payload["provider_failure_kind"]),
	})
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
