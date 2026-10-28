package bashsource

import (
	"encoding/json"
	"errors"
	"strings"
)

// WorkerStartupPolicy describes a server-owned supervisor, never a tool input.
type WorkerStartupPolicy string

const CanonicalBashStdlibV1 WorkerStartupPolicy = "canonical-bash-stdlib-v1"

func ValidateWorkerStartup(policy WorkerStartupPolicy, generation, kind, language string) error {
	if policy == "" && generation == "" {
		return nil // Unchanged legacy sessions do not acquire this capability.
	}
	if policy != CanonicalBashStdlibV1 || kind != "bash" || language != "python" ||
		len(generation) != 64 || strings.Trim(generation, "0123456789abcdef") != "" {
		return errors.New("kernel supervisor startup identity is invalid")
	}
	return nil
}

// ValidateWorkerFrame is shared by every host writer, including generic Execute.
// This profile has no host RPC channel. Execution source must be the complete
// canonical envelope; a marker or Bash kind never authorizes arbitrary Python.
func ValidateWorkerFrame(policy WorkerStartupPolicy, payload []byte) error {
	if policy == "" {
		return nil
	}
	if policy != CanonicalBashStdlibV1 {
		return errors.New("unknown kernel supervisor startup policy")
	}
	var frame struct {
		Type        string  `json:"type"`
		Code        *string `json:"code"`
		HostEnabled bool    `json:"host_enabled"`
	}
	if err := json.Unmarshal(payload, &frame); err != nil {
		return errors.New("restricted supervisor request is invalid")
	}
	if frame.Type != "" || frame.Code == nil {
		return errors.New("restricted supervisor requires a canonical Bash command without host calls")
	}
	return ValidateWorkerExecution(policy, *frame.Code, frame.HostEnabled)
}

func ValidateWorkerExecution(policy WorkerStartupPolicy, code string, hostEnabled bool) error {
	if policy == "" {
		return nil
	}
	if policy != CanonicalBashStdlibV1 || hostEnabled {
		return errors.New("restricted supervisor requires a canonical Bash command without host calls")
	}
	if _, err := BashCommandFromWrapper(code); err != nil {
		return errors.New("restricted supervisor requires a canonical Bash command without host calls")
	}
	return nil
}
