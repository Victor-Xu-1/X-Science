package server

import (
	"encoding/json"
	"sort"
	"x-science/internal/xsciencelink"
	"time"
)

func rememberedApprovalDecisionsFromSetting(value any) map[string]xsciencelink.RememberedApprovalDecision {
	if value == nil {
		return map[string]xsciencelink.RememberedApprovalDecision{}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return map[string]xsciencelink.RememberedApprovalDecision{}
	}
	decisions := map[string]xsciencelink.RememberedApprovalDecision{}
	if err := json.Unmarshal(raw, &decisions); err != nil {
		return map[string]xsciencelink.RememberedApprovalDecision{}
	}
	return decisions
}

func rememberedApprovalDecisionsToSetting(decisions map[string]xsciencelink.RememberedApprovalDecision) map[string]any {
	result := make(map[string]any, len(decisions))
	keys := make([]string, 0, len(decisions))
	for key := range decisions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		decision := decisions[key]
		result[key] = map[string]any{
			"userId":    decision.UserID,
			"clientId":  decision.ClientID,
			"deviceId":  decision.DeviceID,
			"action":    decision.Action,
			"reason":    decision.Reason,
			"createdAt": decision.CreatedAt.UTC().Format(time.RFC3339Nano),
		}
	}
	return result
}

func (s *Server) rememberedApprovalDecisionCount() int {
	if s.settingsStore == nil {
		return 0
	}
	setting, ok, err := s.settingsStore.Get(approvalRememberedSettingKey)
	if err != nil || !ok {
		return 0
	}
	return len(rememberedApprovalDecisionsFromSetting(setting.Value))
}
