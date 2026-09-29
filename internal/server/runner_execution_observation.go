package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// This observes completed interpreter results, not arbitrary shell syntax.
// It never caches execution or grants permission to repeat a side effect.
// A fresh result/variable/figure remains evidence even without a file write.
func (run *sessionRunnerChatRun) observeExecutionEvidence(name string, value any) any {
	if run == nil {
		return value
	}
	switch name {
	case "bash", "python", "r", "repl":
	default:
		return value
	}
	object, ok := value.(map[string]any)
	if !ok || !sessionRunnerReadReuseResultEligible(object) {
		return value
	}
	// Only managed executions with complete workspace observation can be compared.
	if _, present := object["files_written"]; !present {
		return value
	}
	for _, key := range []string{"files_written", "dropped_roots", "artifacts"} {
		if raw := object[key]; raw != nil {
			encoded, err := json.Marshal(raw)
			if err != nil || string(encoded) != "[]" {
				return value
			}
		}
	}
	observed := copyMapAny(object)
	for _, key := range []string{"exec_id", "tool_use_id", "cell_index", "kernel_reused", "effect", "reused", "elapsed_ms", "duration_ms"} {
		delete(observed, key)
	}
	raw, err := json.Marshal(observed)
	if err != nil || len(raw) > maxSessionRunnerReadReuseEntry {
		return value
	}
	digest := sha256.Sum256(raw)
	key := "execution-observation:" + name + ":" + hex.EncodeToString(digest[:])
	_, seen := run.readReuseCache().store(key, sessionRunnerReadReuseEntry{observationDigest: key, size: int64(len(key))})
	if !seen {
		return value
	}
	result := copyMapAny(object)
	// This is solely an evidence observation. Do not claim the interpreter
	// performed no external side effects or prevent a later authorized call.
	result["recovery_evidence_repeated"] = true
	return result
}

func runnerGenerationToolProgress(name string, result any) bool {
	if mapValue(result)["recovery_evidence_repeated"] == true {
		return false
	}
	return runnerToolCompletionHasMaterialProgress(name, result)
}
