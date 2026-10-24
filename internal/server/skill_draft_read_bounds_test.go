package server

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type countedSkillDraftReader struct {
	source io.Reader
	read   int
}

func (reader *countedSkillDraftReader) Read(buffer []byte) (int, error) {
	n, err := reader.source.Read(buffer)
	reader.read += n
	return n, err
}

func TestSkillDraftSnapshotByteReadBounds(t *testing.T) {
	for _, test := range []struct {
		name            string
		size, remaining int64
		content         string
		code            string
		read            int
	}{
		{"fresh_stat_over_remaining", 4, 3, "abcd", "resource_budget_exceeded", 0},
		{"grows_during_read", 1, 3, "abcdefghij", "resource_budget_exceeded", 4},
		{"size_changes_inside_budget", 1, 3, "ab", "unreadable_resource", 2},
		{"exact_remaining", 3, 3, "abc", "", 3},
		{"empty_at_exhausted_budget", 0, 0, "", "", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &countedSkillDraftReader{source: strings.NewReader(test.content)}
			raw, err := readSkillDraftResourceBytes(reader, test.size, test.remaining)
			if test.code == "" {
				if err != nil || string(raw) != test.content {
					t.Fatalf("bounded read=%q %v", raw, err)
				}
			} else {
				var failure *compatibilitySkillValidationError
				if !errors.As(err, &failure) || failure.code != test.code {
					t.Fatalf("error=%v, want %s", err, test.code)
				}
			}
			if reader.read != test.read {
				t.Fatalf("read %d bytes, want hard bound %d", reader.read, test.read)
			}
		})
	}
	first, err := readSkillDraftResourceBytes(strings.NewReader("abc"), 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	second := &countedSkillDraftReader{source: strings.NewReader("def")}
	if _, err := readSkillDraftResourceBytes(second, 3, 5-int64(len(first))); err == nil || second.read != 0 {
		t.Fatalf("actual captured bytes did not reduce the next budget: %v, read=%d", err, second.read)
	}
}
