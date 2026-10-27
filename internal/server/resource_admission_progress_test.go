package server

import (
	"encoding/json"
	"testing"

	"x-science/internal/toolprogress"
)

func TestPublicToolProgressCarriesBoundedResourceAdmission(t *testing.T) {
	snapshot := &toolprogress.ResourceAdmissionSnapshot{
		DecisionAt: "2026-01-02T03:04:05Z", Reason: toolprogress.ResourceAdmissionFairness,
		CapacityAvailableBytes: 900, NetAvailableBytes: 900, RequestedBytes: 400, RequiredBytes: 400, PrecedingWaiters: 1,
	}
	payload := publicToolProgressPayload(toolprogress.Update{Phase: "waiting_for_resources", Indeterminate: true, ResourceAdmission: snapshot}, 0)
	snapshot.PrecedingWaiters = 99
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Phase     string                                  `json:"phase"`
		Admission *toolprogress.ResourceAdmissionSnapshot `json:"resourceAdmission"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Phase != "waiting_for_resources" || decoded.Admission == nil || decoded.Admission.PrecedingWaiters != 1 {
		t.Fatalf("existing JSON progress projection lost or aliased its evidence: %s err=%v", raw, err)
	}
	snapshot.Reason = "private-runtime-path"
	invalid := publicToolProgressPayload(toolprogress.Update{Phase: "waiting_for_resources", ResourceAdmission: snapshot}, 0)
	if _, found := invalid["resourceAdmission"]; found {
		t.Fatal("unbounded telemetry escaped the public whitelist")
	}
}
