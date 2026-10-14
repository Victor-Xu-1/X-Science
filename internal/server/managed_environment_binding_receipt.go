package server

// Every ready recommendation, synchronous reuse and managed operation records
// the same admitted input. The consumer must not reconstruct an implementation
// identity from a proposed display name or its later mutable task selection.
func managedEnvironmentBindingReceipt(tool string, input map[string]any) map[string]any {
	admitted := make(map[string]any, len(input))
	for key, value := range input {
		admitted[key] = value
	}
	return map[string]any{"version": 1, "tool": tool, "input": admitted}
}
