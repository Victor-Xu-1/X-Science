package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// Historical tags describe original stored contracts, never current writes.
// Callers use these readers only after IsHistoricalNamespaceEvent verifies the
// same repository's frozen native source. New admissions remain canonical-only.
const historicalCorrectionConditionSchema = "synon.runner_correction_condition.v1"

func DecodeHistoricalAskUserPromptV1(raw []byte) (AskUserPromptV1, error) {
	var prompt AskUserPromptV1
	if err := decodeAskUserEvent(raw, &prompt); err != nil {
		return prompt, err
	}
	for index, question := range prompt.Questions {
		if question.StageProgress != nil {
			progress, err := NormalizeHistoricalAskUserStageProgress(question.StageProgress)
			if err != nil {
				return prompt, err
			}
			prompt.Questions[index].StageProgress = progress
		}
	}
	encoded, err := json.Marshal(prompt)
	if err != nil {
		return prompt, err
	}
	return DecodeAskUserPromptV1(encoded)
}

func decodeHistoricalRunnerCorrectionCondition(raw []byte) (RunnerCorrectionCondition, error) {
	var condition RunnerCorrectionCondition
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&condition); err != nil {
		return condition, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return condition, errors.New("correction condition has trailing data")
	}
	if condition.Schema == historicalCorrectionConditionSchema {
		condition.historicalSchema = condition.Schema
		condition.Schema = RunnerCorrectionConditionSchema
	}
	return condition, condition.Validate()
}

func ParseHistoricalRunnerInterruptionCause(payload map[string]any) (RunnerInterruptionCause, bool, error) {
	value, present := payload[RunnerInterruptionCauseField]
	if !present {
		return RunnerInterruptionCause{}, false, nil
	}
	record, ok := value.(map[string]any)
	if !ok {
		return RunnerInterruptionCause{}, true, errors.New("runner interruption cause schema is invalid")
	}
	copyRecord := make(map[string]any, len(record))
	for key, value := range record {
		copyRecord[key] = value
	}
	switch record["schema"] {
	case "synon.runner_interruption_cause.v1":
		copyRecord["schema"] = runnerInterruptionCauseSchema
	case "synon.runner_interruption_cause.v2":
		copyRecord["schema"] = "x-science.runner_interruption_cause.v2"
	}
	var historicalCondition *RunnerCorrectionCondition
	if conditionRaw, found := record["condition"]; found {
		raw, err := json.Marshal(conditionRaw)
		if err != nil {
			return RunnerInterruptionCause{}, true, err
		}
		condition, err := decodeHistoricalRunnerCorrectionCondition(raw)
		if err != nil {
			return RunnerInterruptionCause{}, true, err
		}
		historicalCondition = &condition
		copyRecord["condition"] = condition
	}
	cause, present, err := ParseRunnerInterruptionCause(map[string]any{RunnerInterruptionCauseField: copyRecord})
	if err == nil && historicalCondition != nil {
		cause.Condition = historicalCondition
	}
	return cause, present, err
}

func NormalizeHistoricalAskUserStageProgress(value any) (*AskUserStageProgressV1, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if fields["schema"] == "synon.plan_stage_progress.v1" {
		fields["schema"] = "x-science.plan_stage_progress.v1"
	}
	return normalizeAskUserStageProgress(fields)
}
