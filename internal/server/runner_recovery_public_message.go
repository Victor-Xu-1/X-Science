package server

// A paused checkpoint can follow the first request, not only repeated recovery.
// Present its closed public cause without forwarding internal/provider detail.
func runnerRecoveryWaitPublicMessage(language, reason string) string {
	if reason == "provider_stream_no_progress" {
		if language == "zh" {
			return "本轮模型未返回可用的公开回复或工具动作，任务已暂停；原目标和已完成结果均已保留。可以继续任务，或切换模型后继续。"
		}
		return "The model did not return usable public text or a tool action in this request. The task is paused with its goal and completed results preserved. You can continue, or select another model before continuing."
	}
	if language == "zh" {
		return "任务已暂停；原目标和已完成结果均已保留。可以查看任务状态，在条件修复、模型切换或补充输入后继续。"
	}
	return "The task is paused with its goal and completed results preserved. Check its status and continue after the blocking condition, model selection, or user input changes."
}
