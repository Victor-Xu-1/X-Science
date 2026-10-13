package server

import (
	"strings"
	"testing"
)

func TestRunnerRecoveryWaitPublicMessageExplainsNoProgressWithoutClaimingRepeatedRecovery(t *testing.T) {
	for _, language := range []string{"en", "zh", "unknown"} {
		message := runnerRecoveryWaitPublicMessage(language, "provider_stream_no_progress")
		if language == "zh" {
			if !strings.Contains(message, "未返回可用的公开回复或工具动作") || !strings.Contains(message, "已保留") {
				t.Fatalf("missing specific Chinese cause: %s", message)
			}
		} else if !strings.Contains(message, "did not return usable public text or a tool action") || !strings.Contains(message, "preserved") {
			t.Fatalf("missing English/default cause: %s", message)
		}
		for _, reason := range []string{"provider_stream_no_progress", "unknown", "SECRET_RAW_ERROR_SENTINEL"} {
			message = runnerRecoveryWaitPublicMessage(language, reason)
			if strings.Contains(message, "连续恢复") || strings.Contains(message, "Recovery has not changed") || strings.Contains(message, "SECRET_RAW_ERROR_SENTINEL") {
				t.Fatalf("pause message invented retry history or forwarded raw detail: %s", message)
			}
		}
	}
}
