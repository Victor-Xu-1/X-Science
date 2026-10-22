package server

import (
	"strings"

	"x-science/internal/agentruntime"
)

func repeatedRuntimeSkillInvocationResult(skillName string, loadedSkills []string, receipt *agentruntime.SkillLoadReceipt) map[string]any {
	return map[string]any{
		"load_receipt": receipt.Clone(),
		"ok":           true,
		"success":      true,
		"loaded":       true,
		"reused":       true,
		"executed":     false,
		"status":       "already_loaded",
		"skill":        strings.TrimSpace(skillName),
		"loadedSkills": append([]string(nil), loadedSkills...),
		"message":      "This capability and argument set is already active for the current task.",
		"nextAction":   "Continue with the requested work or provide the requested final answer. Do not search for or load the same capability again unless the task or arguments change.",
	}
}
