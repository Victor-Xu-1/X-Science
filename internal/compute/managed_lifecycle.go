package compute

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const maxTranscriptBytes = 64 << 10

// Keep historical fields and order unchanged. Omitted owner bytes preserve the
// exact historical approval; ownership must be approved and proved separately.
type ManagedEndpointRegistration struct {
	Name           string                    `json:"name"`
	URL            string                    `json:"url"`
	Port           int                       `json:"port"`
	CredentialName string                    `json:"credentialName,omitempty"`
	SkillName      string                    `json:"skillName"`
	StartScript    string                    `json:"startScript"`
	StopScript     string                    `json:"stopScript"`
	LivePath       string                    `json:"livePath"`
	Owner          *ManagedEndpointOwnerSpec `json:"owner,omitempty"`
}

func ApprovedManagedEndpointHash(registration ManagedEndpointRegistration) string {
	raw, _ := json.Marshal(registration)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type cappedBuffer struct{ bytes.Buffer }

func (buffer *cappedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := maxTranscriptBytes - buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = buffer.Buffer.Write(value)
	}
	return original, nil
}
