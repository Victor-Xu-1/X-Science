package workspace

// Presentation belongs to one conversation identity, not its copied history.
// Copy both maps before deleting; retain unknown/legacy values verbatim.
func cloneConversationPresentationContext(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	if extra, ok := source["web_extra"].(map[string]any); ok {
		copyExtra := make(map[string]any, len(extra))
		for key, value := range extra {
			copyExtra[key] = value
		}
		for _, key := range []string{"pinned", "pinned_at", "sortOrder", "archived_at", "presentation_unavailable", presentationRevisionKey} {
			delete(copyExtra, key)
		}
		result["web_extra"] = copyExtra
	}
	return result
}

func conversationCloneComparableContext(source map[string]any) map[string]any {
	result := cloneConversationPresentationContext(source)
	// Missing and emptied presentation-only envelopes are equivalent for a
	// clone receipt. This changes neither source nor stored target metadata.
	if extra, ok := result["web_extra"].(map[string]any); ok && len(extra) == 0 {
		delete(result, "web_extra")
	}
	return result
}
