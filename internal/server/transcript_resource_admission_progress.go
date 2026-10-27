package server

import (
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/toolprogress"
)

func transcriptResourceAdmissionProgress(value any, phase string) (map[string]any, error) {
	raw, ok := value.(map[string]any)
	if !ok || len(raw) != 9 {
		return nil, transcriptstore.ErrEventConflict
	}
	decision, decisionOK := raw["decisionAt"].(string)
	reason, reasonOK := raw["reason"].(string)
	if !decisionOK || !reasonOK {
		return nil, transcriptstore.ErrEventConflict
	}
	snapshot := toolprogress.ResourceAdmissionSnapshot{DecisionAt: decision, Reason: reason}
	fields := []struct {
		name  string
		value *int64
	}{
		{"capacityAvailableBytes", &snapshot.CapacityAvailableBytes},
		{"unusedPromisedBytes", &snapshot.UnusedPromisedBytes},
		{"netAvailableBytes", &snapshot.NetAvailableBytes},
		{"requestedBytes", &snapshot.RequestedBytes},
		{"requiredBytes", &snapshot.RequiredBytes},
		{"reservedBytes", &snapshot.ReservedBytes},
		{"precedingWaiters", &snapshot.PrecedingWaiters},
	}
	for _, field := range fields {
		value, ok := transcriptWebNonnegativeSafeInteger(raw[field.name])
		if !ok {
			return nil, transcriptstore.ErrEventConflict
		}
		*field.value = value
	}
	normalized := toolprogress.NormalizeResourceAdmission(&snapshot)
	if normalized == nil || normalized.DecisionAt != decision ||
		!(phase == "waiting_for_resources" && reason != toolprogress.ResourceAdmissionGranted ||
			phase == "starting" && reason == toolprogress.ResourceAdmissionGranted) {
		return nil, transcriptstore.ErrEventConflict
	}
	result := map[string]any{"decisionAt": decision, "reason": reason}
	for _, field := range fields {
		result[field.name] = raw[field.name]
	}
	return result, nil
}

func copyTranscriptResourceProgress(progress map[string]any, status string) map[string]any {
	if progress == nil {
		return nil
	}
	result := make(map[string]any, len(progress))
	for key, value := range progress {
		result[key] = value
	}
	if value, present := progress["resourceAdmission"]; present {
		admission, err := transcriptResourceAdmissionProgress(value, webString(progress["phase"]))
		if err != nil || !transcriptToolStatusActive(status) {
			delete(result, "resourceAdmission")
		} else {
			result["resourceAdmission"] = admission
		}
	}
	return result
}

func mergeTranscriptResourceProgress(current, update transcriptToolHistoryFact) (map[string]any, bool) {
	if !update.progressSet {
		result := copyTranscriptResourceProgress(current.progress, update.status)
		delete(result, "resourceAdmission")
		return result, current.progressSet
	}
	result := copyTranscriptResourceProgress(update.progress, update.status)
	_, hasAdmission := result["resourceAdmission"]
	sameScope := current.progressStreamUID != "" && current.progressStreamUID == update.progressStreamUID &&
		current.attempt == update.attempt && transcriptToolExecutionAttempt(current) == transcriptToolExecutionAttempt(update) &&
		current.callID == update.callID && current.name == update.name &&
		current.inputSet && (!update.inputSet || transcriptToolJSONEqual(current.input, update.input))
	if !hasAdmission && sameScope && transcriptToolStatusActive(current.status) && transcriptToolStatusActive(update.status) &&
		current.progressSourceRevision > 0 && update.progressSourceRevision >= current.progressSourceRevision &&
		webString(current.progress["phase"]) == webString(update.progress["phase"]) {
		if admission, err := transcriptResourceAdmissionProgress(current.progress["resourceAdmission"], webString(current.progress["phase"])); err == nil {
			result["resourceAdmission"] = admission
		}
	}
	return result, true
}
