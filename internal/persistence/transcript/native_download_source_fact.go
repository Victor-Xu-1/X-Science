package transcript

import "encoding/json"

const NativeDownloadSourceAcquisitionSchema = "x-science.native-download-source.v1"

// Source acquisition and selection are private phases of their admitted
// download, not root model tool lifecycles. Reserve the shared schema even when
// the remaining fields are damaged; the domain reader validates the proof.
func isNativeDownloadSourceAcquisitionFact(event Event, payload []byte) bool {
	if event.Type != "runner_checkpoint" {
		return false
	}
	if len(payload) == 0 {
		payload = event.PayloadJSON
	}
	var fact struct {
		Schema string `json:"schema"`
	}
	return json.Unmarshal(payload, &fact) == nil && fact.Schema == NativeDownloadSourceAcquisitionSchema
}
