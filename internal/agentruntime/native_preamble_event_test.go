package agentruntime

import "testing"

func TestNativePreambleEventRetainsStrictContentAndIdentityBoundary(t *testing.T) {
	valid := ModelStreamEvent{Kind: ModelStreamEventNativePreambleBoundary, BlockID: "native-identified-prefix"}
	if err := ValidateModelStreamEvent(valid); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []ModelStreamEvent{
		{Kind: ModelStreamEventNativePreambleBoundary},
		{Kind: ModelStreamEventNativePreambleBoundary, BlockID: "invalid block id"},
		{Kind: ModelStreamEventNativePreambleBoundary, BlockID: valid.BlockID, ContentDelta: "not boundary data"},
		{Kind: ModelStreamEventNativePreambleBoundary, BlockID: valid.BlockID, ReasoningActive: true},
	} {
		if err := ValidateModelStreamEvent(invalid); err == nil {
			t.Fatalf("invalid native boundary accepted: %+v", invalid)
		}
	}
}
