package transcript

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestFrameMessageIntentJSONValuePreservesProofAcrossQueueReadback(t *testing.T) {
	for _, branch := range []string{"", "br_1234abcd"} {
		t.Run("branch="+branch, func(t *testing.T) {
			intent := FrameMessageIntent{
				Version: 1, OwnerID: "owner", FrameID: "frame", FrameIncarnationID: "incarnation",
				LoadingID: "loading", InputMessageID: FrameMessageIntentID("frame", "loading", branch),
				TargetBranchID: branch, RequestSHA256: strings.Repeat("a", 64),
			}
			if !intent.Valid() {
				t.Fatal("invalid test proof")
			}
			original, err := json.Marshal(intent)
			if err != nil {
				t.Fatal(err)
			}
			var persisted map[string]any
			if err := json.Unmarshal(original, &persisted); err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal(persisted)
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := json.Marshal(intent.JSONValue())
			if err != nil || !bytes.Equal(fresh, want) {
				t.Fatalf("fresh queue proof differs from readback: %s != %s (%v)", fresh, want, err)
			}
			if err := json.Unmarshal(fresh, &persisted); err != nil {
				t.Fatal(err)
			}
			repeated, err := json.Marshal(persisted)
			if err != nil || !bytes.Equal(fresh, repeated) {
				t.Fatalf("queue readback changed proof bytes: %s != %s (%v)", fresh, repeated, err)
			}
			decoded, err := DecodeFrameMessageIntent(persisted)
			if err != nil || decoded == nil || *decoded != intent {
				t.Fatalf("queue readback lost typed proof: %#v %v", decoded, err)
			}
		})
	}
}
