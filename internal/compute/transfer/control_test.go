package transfer

import (
	"strings"
	"testing"
)

func TestOutputControlRequiresExplicitCompleteReceipts(t *testing.T) {
	for _, raw := range []string{"", "ready", "ready::0", "ready:" + strings.Repeat("a", 64) + ":-1", "failed:0", "failed:256", "banner\nworking"} {
		if _, err := ParseControlReceipt(raw); err == nil {
			t.Fatalf("malformed receipt produced a state: %q", raw)
		}
	}
	for _, raw := range []string{"working", "unknown", "failed:76", "ready:" + strings.Repeat("a", 64) + ":0"} {
		if _, err := ParseControlReceipt(raw); err != nil {
			t.Fatalf("explicit receipt rejected: %q: %v", raw, err)
		}
	}
}
