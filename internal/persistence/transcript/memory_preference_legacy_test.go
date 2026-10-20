package transcript

import "testing"

func TestMemoryPreferenceAdmissionLeavesLegacyRetryUnchanged(t *testing.T) {
	repo, db, stream := memoryAdmissionFixture(t, `{"_original_input":{"memory_mode":"on"}}`)
	forceLegacyFrameAuthorityForTest(t, db, stream)
	input := memoryAdmissionInput(stream, "legacy")
	event, frame, created, err := repo.AppendFrameUserEvent(t.Context(), input)
	if err != nil || !created || event.Source != EventSourceFrameRef {
		t.Fatalf("legacy first=%+v created=%t err=%v", event, created, err)
	}
	retry, replay, created, err := repo.AppendFrameUserEvent(t.Context(), input)
	if err != nil || created || retry.EventID != event.EventID || string(replay.PayloadJSON) != string(frame.PayloadJSON) {
		t.Fatalf("legacy omitted retry changed: created=%t event=%+v err=%v", created, retry, err)
	}
}
