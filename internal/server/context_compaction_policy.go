package server

import "errors"

type contextCompactionPolicy struct {
	Enabled         bool    `json:"enabled"`
	WindowTokens    int     `json:"windowTokens"`
	ThresholdTokens int     `json:"thresholdTokens"`
	Percent         float64 `json:"percent"`
	Source          string  `json:"source"`
}

// One threshold calculation is shared by preflight, assembled-request admission
// and read-only UI policy. Explicit overrides are shown rather than hidden.
func resolveContextCompactionThreshold(window int, setting any, found bool) (int, string) {
	if found {
		if value := int(numberValue(setting)); value > 0 {
			if window > 0 {
				value = min(value, window) // An explicit budget cannot enlarge model capacity.
			}
			return value, "token_override"
		}
	}
	if window <= 0 {
		return 0, "unknown"
	}
	return max(1, window*defaultRunnerAutoCompactContextPercent/100), "window_percent"
}

func (s *Server) contextCompactionPolicy(window int) (*contextCompactionPolicy, error) {
	if s == nil || s.settingsStore == nil {
		return nil, errors.New("context policy storage unavailable")
	}
	enabled, exists, err := s.settingsStore.Get(configStoreKey("autoCompactEnabled"))
	if err != nil {
		return nil, err
	}
	isEnabled := !exists || boolValue(enabled.Value, false)
	setting, found, err := s.settingsStore.Get(configStoreKey("autoCompactTokenThreshold"))
	if err != nil {
		return nil, err
	}
	threshold, source := resolveContextCompactionThreshold(window, setting.Value, found)
	percent := 0.0 // Not a fullness percentage when the model capacity is unknown.
	if window > 0 {
		percent = float64(threshold) * 100 / float64(window)
	}
	return &contextCompactionPolicy{Enabled: isEnabled, WindowTokens: window, ThresholdTokens: threshold,
		Percent: percent, Source: source}, nil
}
