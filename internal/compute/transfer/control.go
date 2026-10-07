package transfer

import (
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

type ControlReceipt struct {
	State    string `json:"state"`
	SHA256   string `json:"sha256,omitempty"`
	Bytes    int64  `json:"bytes,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
}

func ParseControlReceipt(raw string) (ControlReceipt, error) {
	raw = strings.TrimSpace(raw)
	if raw == "working" || raw == "unknown" {
		return ControlReceipt{State: raw}, nil
	}
	parts := strings.Split(raw, ":")
	if len(parts) == 2 && parts[0] == "failed" {
		code, err := strconv.Atoi(parts[1])
		if err == nil && code > 0 && code < 256 {
			return ControlReceipt{State: "failed", ExitCode: code}, nil
		}
	}
	if len(parts) == 3 && parts[0] == "ready" {
		digest, err := hex.DecodeString(parts[1])
		bytes, sizeErr := strconv.ParseInt(parts[2], 10, 64)
		if err == nil && sizeErr == nil && len(digest) == 32 && len(parts[1]) == 64 &&
			parts[1] == strings.ToLower(parts[1]) && bytes >= 0 {
			return ControlReceipt{State: "ready", SHA256: parts[1], Bytes: bytes}, nil
		}
	}
	return ControlReceipt{}, errors.New("remote output-control receipt is invalid")
}
