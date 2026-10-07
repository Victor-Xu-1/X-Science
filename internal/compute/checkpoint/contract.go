package checkpoint

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"unicode/utf8"
)

const Schema = "synon.compute-checkpoint.v1"

// This declares an application-native, file-backed recovery mechanism. It
// does not assert that arbitrary process memory or a software package can be
// reconstructed. The exact resume command is approved with the submission.
type Contract struct {
	Manifest      string `json:"manifest"`
	ResumeCommand string `json:"resume_command"`
	Signal        string `json:"signal"`
	PIDFile       string `json:"pid_file,omitempty"`
}

func Decode(raw any) (*Contract, error) {
	if raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil || len(encoded) > 263168 {
		return nil, errors.New("checkpoint control contract is oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var result Contract
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	if !ValidPath(result.Manifest) || result.ResumeCommand == "" || !utf8.ValidString(result.ResumeCommand) || len(result.ResumeCommand) > 262144 || strings.ContainsRune(result.ResumeCommand, 0) {
		return nil, errors.New("checkpoint manifest or resume command is invalid")
	}
	switch result.Signal {
	case "TERM", "USR1", "USR2":
	default:
		return nil, errors.New("checkpoint signal must name a declared application-native TERM, USR1 or USR2 handler")
	}
	if result.Signal != "TERM" && !ValidPath(result.PIDFile) {
		return nil, errors.New("a native checkpoint signal requires an incarnation-bound pid_file under out/")
	}
	return &result, nil
}

func ValidPath(name string) bool {
	return len(name) > 4 && len(name) <= 4096 && utf8.ValidString(name) && strings.HasPrefix(name, "out/") && path.Clean(name) == name && !strings.ContainsRune(name, 0)
}
func (c Contract) CommandSHA256() string {
	digest := sha256.Sum256([]byte(c.ResumeCommand))
	return hex.EncodeToString(digest[:])
}
