package server

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReviewerUTF8BlocksKeepContentAtExtremeLimits(t *testing.T) {
	value := "alpha中文omega"
	for _, limit := range []int{0, 4, 7, int(^uint(0) >> 1)} {
		parts := splitReviewerUTF8Blocks(value, limit)
		if strings.Join(parts, "") != value {
			t.Fatalf("limit=%d lost source content", limit)
		}
		for _, part := range parts {
			if !utf8.ValidString(part) || (limit > 0 && len(part) > limit) {
				t.Fatalf("limit=%d invalid block=%q", limit, part)
			}
		}
	}
}
