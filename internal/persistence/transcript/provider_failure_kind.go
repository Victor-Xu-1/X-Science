package transcript

import "errors"

// ValidRunnerProviderFailureKind validates optional presentation data on the
// existing interruption. It does not change the reason, lease or resume rules.
func ValidRunnerProviderFailureKind(kind string) bool {
	switch kind {
	case "configuration_missing", "subscription", "authentication", "access_denied", "unavailable":
		return true
	default:
		return false
	}
}

func validateRunnerProviderFailureKind(reason, kind string) error {
	if kind == "" {
		return nil
	}
	if reason != "model_provider_unavailable" || !ValidRunnerProviderFailureKind(kind) {
		return errors.New("runner provider failure category is invalid")
	}
	return nil
}
