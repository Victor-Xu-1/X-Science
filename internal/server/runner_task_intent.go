package server

import (
	"errors"
	"strings"
)

// applySessionRunnerTaskIntent keeps live execution and immutable completion
// recovery on the same task identity and language contract. Callers resolve the
// intent from their admitted Transcript authority, never candidate prose.
func applySessionRunnerTaskIntent(run *sessionRunnerChatRun, text, language, id string, revision int64) error {
	text = strings.TrimSpace(text)
	id = strings.TrimSpace(id)
	if run == nil || text == "" || id == "" || revision <= 0 {
		return errors.New("canonical transcript task intent is unavailable")
	}
	run.TaskIntent = text
	run.TaskIntentID = id
	run.TaskIntentRevision = revision
	run.ResponseLanguage = canonicalSessionRunnerResponseLanguage(language)
	return nil
}
