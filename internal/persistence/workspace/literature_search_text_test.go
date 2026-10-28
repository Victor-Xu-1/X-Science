package workspace

import (
	"database/sql"
	"testing"
)

// Frozen boundary outputs from the reference NFKC/ECMAScript-whitespace/default
// lowercase expression executed in JavaScript. Finite vectors do not assert
// equivalence for every Unicode version or all Unicode strings.
func TestLiteratureSearchTextReferenceVectors(t *testing.T) {
	for _, example := range []struct{ name, input, want string }{
		{"empty", "", ""},
		{"width-ligature", "  \uff26\uff29\uff32\uff33\uff34\u00a0\ufb03  ", "first ffi"},
		{"feff", "\ufeffAlpha\ufeff\u0009Beta\ufeff", "alpha beta"},
		{"ecma-spaces", "\u1680A\u2000\u200aB\u2028\u2029C\u202f\u205fD\u3000", "a b c d"},
		{"not-ecma-0085", "\u0085A\u0085B\u0085", "\u0085a\u0085b\u0085"},
		{"not-ecma-zero-width", "\u180eA\u200bB\u180e", "\u180ea\u200bb\u180e"},
		{"sigma-final", "\u039f\u03a3", "\u03bf\u03c2"},
		{"sigma-medial", "\u039f\u03a3\u0391", "\u03bf\u03c3\u03b1"},
		{"sigma-ignorable-final", "\u039f\u03a3\u0301", "\u03bf\u03c2\u0301"},
		{"sigma-ignorable-medial", "\u039f\u03a3\u0301\u0391", "\u03bf\u03c3\u0301\u03b1"},
		{"sigma-noncased", "\u039f\u03a31", "\u03bf\u03c21"},
		{"sigma-no-preceding", "1\u03a3", "1\u03c3"},
		{"sigma-zwj", "\u039f\u03a3\u200d", "\u03bf\u03c2\u200d"},
		{"sigma-apostrophe", "\u039f\u03a3\u2019\u0391", "\u03bf\u03c3\u2019\u03b1"},
		{"dotted-I", "\u0130 I \u0131", "i\u0307 i \u0131"},
		{"sharp-s", "\u1e9e SS \u00df", "\u00df ss \u00df"},
		{"canonical", "A\u030a\u0009\u00c5", "\u00e5 \u00e5"},
		{"astral", "\U00010400\U00010428", "\U00010428\U00010428"},
		{"literal", "100%_ O'Brien", "100%_ o'brien"},
	} {
		t.Run(example.name, func(t *testing.T) {
			if got := normalizeLiteratureSearchText(example.input); got != example.want {
				t.Fatalf("normalize(%q)=%q, want %q", example.input, got, example.want)
			}
		})
	}
}

func TestLiteratureSearchTextUsesOriginalWriterReadAndExistingPools(t *testing.T) {
	store, path := literatureCollectionStoreFixture(t)
	check := func(database *sql.DB) {
		t.Helper()
		var got string
		if err := database.QueryRow(`SELECT xscience_literature_search_text_v1(?)`, "\ufeffΟΣ\u00a0ＦＩＲＳＴ").Scan(&got); err != nil || got != "ος first" {
			t.Fatalf("original connection missing the registered normalizer: %q %v", got, err)
		}
	}
	check(store.db)
	check(store.readDB)
	// Pin two separate reader connections, then retire their idle connections.
	// Every later connection must inherit the function from the original driver.
	store.readDB.SetMaxIdleConns(0)
	var readers []*sql.Conn
	for range 2 {
		conn, err := store.readDB.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		readers = append(readers, conn)
		var got string
		if err := conn.QueryRowContext(t.Context(), `SELECT xscience_literature_search_text_v1(?)`, "ΟΣ").Scan(&got); err != nil || got != "ος" {
			t.Fatalf("reader connection lost contextual normalization: %q %v", got, err)
		}
	}
	for _, conn := range readers {
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
	check(store.readDB)
	existing, err := OpenExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = existing.Close() })
	check(existing.db)
	check(existing.readDB)
}
