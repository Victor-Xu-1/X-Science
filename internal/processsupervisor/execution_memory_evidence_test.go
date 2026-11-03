package processsupervisor

import (
	"testing"
	"time"
)

func TestExecutionMemoryEvidenceDistinguishesZeroUnlimitedAndUnknown(t *testing.T) {
	zero, one := uint64(0), uint64(1)
	known := MemoryEvidenceValue{Kind: "bytes", Bytes: &zero}
	unlimited := MemoryEvidenceValue{Kind: "unlimited"}
	unknown := MemoryEvidenceValue{Kind: "unavailable", Reason: "read_failed"}
	if !known.valid(false) || !unlimited.valid(true) || unlimited.valid(false) || !unknown.valid(false) {
		t.Fatal("value semantics lost")
	}
	if (MemoryEvidenceValue{Kind: "bytes"}).valid(false) || (MemoryEvidenceValue{Kind: "unavailable", Bytes: &zero, Reason: "read_failed"}).valid(false) {
		t.Fatal("unknown acquired numeric value")
	}
	for _, reason := range []string{"legacy_v1", "no_sampler", "unsupported_platform", "no_owned_domain", "unproven_container_domain"} {
		if err := UnavailableExecutionMemoryEvidence(reason).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	continuous := MemoryEvidenceCounter{Begin: &zero, End: &one, Delta: &one, Continuity: "continuous"}
	reset := MemoryEvidenceCounter{Begin: &one, End: &zero, Continuity: "reset"}
	if !continuous.valid() || !reset.valid() || (MemoryEvidenceCounter{Begin: &one, End: &zero, Delta: &zero, Continuity: "continuous"}).valid() {
		t.Fatal("counter continuity lost")
	}
	now := time.Now().UTC()
	sample := MemoryEvidenceSample{At: now, Current: known, Max: unlimited, High: unknown, SwapMax: known}
	evidence := ExecutionMemoryEvidenceV1{Version: 1, Status: "observed", Scope: "workload_domain", Window: "dispatch_to_settlement", Begin: &sample, End: &sample,
		Events: &MemoryEvidenceEvents{OOM: continuous, OOMKill: reset, Max: continuous, High: MemoryEvidenceCounter{Continuity: "unavailable"}}}
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	evidence.Window = "this_child_exclusively"
	if evidence.Validate() == nil {
		t.Fatal("unsupported attribution accepted")
	}
}
