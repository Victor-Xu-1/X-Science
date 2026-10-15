package server

import "strings"

const (
	completionAdvisoryStructurePreview = "structure_preview_unverified"
	completionAdvisoryResultQuality    = "result_quality_unverified"
)

// Completion advisories are host findings, not another completion obligation.
// They are rendered into the same final assistant content before persistence;
// no separate event, model repair, or mutable UI-only status is necessary.
type sessionRunnerCompletionAdvisory struct {
	Code     string
	Findings []string
}

func appendSessionRunnerCompletionAdvisories(content, language string, advisories ...*sessionRunnerCompletionAdvisory) string {
	// A notice must not turn a missing model answer into a completed response.
	if strings.TrimSpace(content) == "" {
		return content
	}
	for _, advisory := range advisories {
		if advisory == nil {
			continue
		}
		english, chinese := sessionRunnerCompletionAdvisoryText(advisory.Code)
		if english == "" || strings.Contains(content, english) || (chinese != "" && strings.Contains(content, chinese)) {
			continue
		}
		notice := english
		if sessionRunnerRequiresChinese(language) {
			notice = chinese
		}
		content += "\n\n> " + notice
	}
	return content
}

func sessionRunnerCompletionAdvisoryText(code string) (english, chinese string) {
	switch code {
	case completionAdvisoryStructurePreview:
		return "Structure preview is unverified. Existing results are delivered without a validated combined structure preview. This does not certify scientific correctness or fulfil any missing preview deliverable.",
			"结构预览尚未验证。现有结果已交付，但不包含经过验证的组合结构预览；这不代表科学结论已验证，也不代表缺少的预览交付项已经完成。"
	case completionAdvisoryResultQuality:
		return "Result quality is unverified. Some consistency or evidence checks remain unresolved. Existing outputs are preserved; task completion does not certify their accuracy.",
			"结果质量尚未验证。部分一致性或证据检查仍存在问题。现有输出已保留；任务完成不代表这些结果的准确性已经得到验证。"
	default:
		return "", ""
	}
}

func sessionRunnerVisualCorrectionIsAdvisory(correction recoveredRunnerCorrection) bool {
	condition := correction.Condition
	if correction.ReasonCode != sessionRunnerVisualArtifactValidationReasonCode || condition == nil ||
		condition.ReasonCode != correction.ReasonCode || condition.Validate() != nil || condition.Visual == nil {
		return false
	}
	// Typed historical image producers always supplied exact Targets, including
	// invalid_digest, missing_validation and invalid_image_integrity. Only the
	// auxiliary scene producer used UnboundNames alone. Untyped historical text,
	// empty conditions and unknown image failures cannot establish this policy.
	return len(condition.Visual.Artifacts) == 0 && strings.TrimSpace(strings.Join(condition.Visual.UnboundNames, "")) != ""
}
