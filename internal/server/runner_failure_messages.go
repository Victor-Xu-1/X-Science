package server

import (
	"net/http"
	"strings"

	"x-science/internal/providers"
)

// publicSessionRunnerFailureMessage separates diagnostic failures from the
// terminal message shown in a conversation. Audit and tool receipts keep the
// exact root cause; the conversation must not expose provider payloads, paths,
// runtime ownership terms, persistence internals, or construction language.
func publicSessionRunnerFailureMessage(raw, language string, reasonCodes ...string) string {
	if len(reasonCodes) > 0 {
		switch reasonCodes[0] {
		case sessionRunnerResponseLanguageMismatchReasonCode, sessionRunnerFinalPresentationReasonCode:
			return localizedSessionRunnerFailureMessage(language, "presentation")
		case "runner_stop_hook_failed":
			return localizedSessionRunnerFailureMessage(language, "finalization")
		case "runner_cleanup_failed":
			return localizedSessionRunnerFailureMessage(language, "cleanup")
		case "runner_response_persistence_failed":
			return localizedSessionRunnerFailureMessage(language, "persistence")
		case sessionRunnerHistoryPreparationFailedReasonCode:
			return localizedSessionRunnerFailureMessage(language, "history_preparation")
		case sessionRunnerPreparationFailedReasonCode:
			return localizedSessionRunnerFailureMessage(language, "preparation")
		}
	}
	message := strings.TrimSpace(raw)
	if message == "" {
		return localizedSessionRunnerFailureMessage(language, "generic")
	}
	normalized := strings.ToLower(message)
	if !sessionRunnerModelProviderUnavailableFailure(message) {
		return localizedSessionRunnerFailureMessage(language, "execution")
	}
	if status, kind, ok := providers.ParseLegacyHTTPFailure(message); ok &&
		(status == http.StatusBadRequest || kind != providers.FailureUnavailable) {
		return publicRunnerProviderFailureDetail(string(kind), language)
	}

	switch {
	case containsSessionRunnerFailureHint(normalized,
		"429", "too many requests", "rate limit", "rate_limit", "rate-limit",
		"quota", "usage limit", "resource exhausted"):
		return localizedSessionRunnerFailureMessage(language, "quota")
	case containsSessionRunnerFailureHint(normalized,
		"401", "403", "unauthorized", "forbidden", "invalid api key", "authentication"):
		return localizedSessionRunnerFailureMessage(language, "authentication")
	case containsSessionRunnerFailureHint(normalized,
		"404", "model_not_found", "model not found"):
		return localizedSessionRunnerFailureMessage(language, "model")
	case containsSessionRunnerFailureHint(normalized,
		"timeout", "deadline exceeded", "context deadline exceeded"):
		return localizedSessionRunnerFailureMessage(language, "timeout")
	case containsSessionRunnerFailureHint(normalized,
		"connection refused", "no such host", "dial tcp", "network"):
		return localizedSessionRunnerFailureMessage(language, "connection")
	default:
		return localizedSessionRunnerFailureMessage(language, "provider")
	}
}

func containsSessionRunnerFailureHint(message string, hints ...string) bool {
	for _, hint := range hints {
		if strings.Contains(message, hint) {
			return true
		}
	}
	return false
}

func localizedSessionRunnerFailureMessage(language, kind string) string {
	if strings.EqualFold(strings.TrimSpace(language), "zh") {
		switch kind {
		case "configuration":
			return "当前模型配置不可用。任务、检查点和已有结果均已保留；请修复当前模型配置或选择已配置的模型后继续。"
		case "subscription":
			return "当前模型服务因订阅或计费状态拒绝了请求。任务、检查点和已有结果均已保留；请续订或修正当前服务账号，或选择其他已配置模型后继续。"
		case "access":
			return "当前模型服务拒绝了账号访问。任务、检查点和已有结果均已保留；请修正当前服务账号的访问权限或选择其他已配置模型后继续。"
		case "presentation":
			return "回复的语言或呈现格式未通过校验。已有文件和执行结果已保留，尚不能视为任务完成；可从保存的位置继续修正回复。"
		case "finalization":
			return "任务收尾检查未完成。已有结果已保留；请检查运行详情后继续。"
		case "cleanup":
			return "任务运行环境的收尾处理失败。已有结果已保留；请检查运行详情后继续。"
		case "persistence":
			return "回复保存失败，不能确认本轮已完整结束。已有文件不会因此被重新生成；请恢复存储后继续。"
		case "history_preparation":
			return "本次运行未能完成对话历史准备。已有进度和结果已保留；请检查运行详情后再继续。"
		case "preparation":
			return "任务准备未完成。已有进度和结果已保留；请检查运行详情后再继续。"
		case "execution":
			return "任务本次运行未能完整结束。当前进度和已有结果均已保存；继续运行会从已保存的位置恢复。"
		case "quota":
			return "当前模型服务已达到额度或速率限制。任务、检查点、失败记录和已生成结果均已保留；切换到其他已配置模型后会从原任务继续。"
		case "authentication":
			return "当前模型服务认证失败。任务、检查点和已生成结果均已保留；修正凭据或切换模型后会从原任务继续。"
		case "model":
			return "当前模型在服务端不可用。任务、检查点和已生成结果均已保留；切换到其他已配置模型后会从原任务继续。"
		case "timeout":
			return "模型服务响应超时。任务、检查点和已生成结果均已保留；稍后继续或切换模型会从原任务恢复。"
		case "connection":
			return "暂时无法连接当前模型服务。任务、检查点和已生成结果均已保留；端点恢复或切换模型后会从原任务继续。"
		default:
			return "当前模型服务暂时不可用。任务、检查点和已生成结果均已保留；稍后继续或切换模型不会重建任务。"
		}
	}

	switch kind {
	case "configuration":
		return "The current model configuration is unavailable. The task, checkpoints, and existing results were preserved; repair the current configuration or select a configured model before continuing."
	case "subscription":
		return "The current model service rejected this request because of its subscription or billing status. The task, checkpoints, and existing results were preserved; renew or correct the current provider account, or choose another configured model before continuing."
	case "access":
		return "The current model service denied account access. The task, checkpoints, and existing results were preserved; correct the current provider account's access or choose another configured model before continuing."
	case "presentation":
		return "The response did not pass language or presentation validation. Existing files and execution results were preserved; the task is not complete. Continue from the saved state to correct the response."
	case "finalization":
		return "Task finalization checks did not finish. Existing results were preserved; inspect the run details before continuing."
	case "cleanup":
		return "Task runtime cleanup failed. Existing results were preserved; inspect the run details before continuing."
	case "persistence":
		return "The response could not be saved, so this run is not confirmed complete. Existing files will not be regenerated; restore storage before continuing."
	case "history_preparation":
		return "Conversation history could not be prepared for this run. Existing progress and results were preserved; inspect the run details before continuing."
	case "preparation":
		return "Task preparation did not finish. Existing progress and results were preserved; inspect the run details before continuing."
	case "execution":
		return "This run did not finish completely. Current progress and existing results were preserved; continuing resumes from the saved position."
	case "quota":
		return "The current model service reached its quota or rate limit. The task, checkpoints, failure record, and generated results were preserved; switching models resumes the same task."
	case "authentication":
		return "Authentication for the current model service failed. The task, checkpoints, and generated results were preserved; fix the credentials or switch models to resume the same task."
	case "model":
		return "The current model is unavailable at the provider. The task, checkpoints, and generated results were preserved; switch models to resume the same task."
	case "timeout":
		return "The model service timed out. The task, checkpoints, and generated results were preserved; continue later or switch models to resume the same task."
	case "connection":
		return "The current model service could not be reached. The task, checkpoints, and generated results were preserved; restore the endpoint or switch models to resume the same task."
	default:
		return "The current model service is temporarily unavailable. The task, checkpoints, and generated results were preserved; continuing later or switching models will not recreate the task."
	}
}
