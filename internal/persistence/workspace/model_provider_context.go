package workspace

import "errors"

func validateModelProviderControls(input ModelProviderInput) error {
	if err := validateModelProviderGenerationControls(input.Temperature, input.MaxTokens); err != nil {
		return err
	}
	if input.ContextWindow != nil && (*input.ContextWindow < 1 || *input.ContextWindow > 10_000_000) {
		return errors.New("model provider context window must be between 1 and 10000000, or null for unknown")
	}
	return nil
}
