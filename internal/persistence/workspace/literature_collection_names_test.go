package workspace

import (
	"errors"
	"strings"
	"testing"
)

func TestLiteratureCollectionTextMatchesReference(t *testing.T) {
	for _, test := range []struct{ name, input, description, want, key, wantDescription string }{
		{"compatibility-and-space", "\ufeff Ｒｅｖｉｅｗ\u00a0\tNotes \u2029", "  Ａ\n\tＢ  ", "Review Notes", "review notes", "A B"},
		{"default-Unicode-lower", "İSTANBUL", "", "İSTANBUL", "i\u0307stanbul", ""},
		{"NEL-is-not-ECMAScript-space", "A\u0085B", "", "A\u0085B", "a\u0085b", ""},
		{"UTF16-boundary", strings.Repeat("😀", 100), strings.Repeat("😀", 500), strings.Repeat("😀", 100), strings.Repeat("😀", 100), strings.Repeat("😀", 500)},
	} {
		t.Run(test.name, func(t *testing.T) {
			name, key, description, err := literatureCollectionName(test.input, test.description)
			if err != nil || name != test.want || key != test.key || description != test.wantDescription {
				t.Fatalf("normalization=%q/%q/%q err=%v", name, key, description, err)
			}
		})
	}
	for _, test := range []struct{ name, description string }{
		{" \ufeff\t ", ""}, {strings.Repeat("😀", 101), ""},
		{"valid", strings.Repeat("😀", 501)}, {string([]byte{0xff}), ""},
	} {
		if _, _, _, err := literatureCollectionName(test.name, test.description); !errors.Is(err, ErrLiteratureInvalid) {
			t.Fatalf("invalid text accepted: name units=%d description bytes=%d err=%v", len(test.name), len(test.description), err)
		}
	}
}
