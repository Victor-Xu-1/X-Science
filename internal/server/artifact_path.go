package server

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// artifactPathSegments keeps escaped slashes inside opaque identities. URL.Path
// is already decoded by net/http and must not be split and unescaped again.
func artifactPathSegments(r *http.Request, prefix string) []string {
	remaining, ok := strings.CutPrefix(r.URL.EscapedPath(), prefix)
	if !ok {
		return nil
	}
	return strings.Split(remaining, "/")
}

func decodeArtifactPathIdentity(segment string) (string, error) {
	identity, err := url.PathUnescape(segment)
	if err != nil || !utf8.ValidString(identity) || strings.TrimSpace(identity) == "" || identity == "." || identity == ".." {
		return "", errors.New("invalid artifact identity")
	}
	for _, character := range identity {
		if unicode.IsControl(character) {
			return "", errors.New("invalid artifact identity")
		}
	}
	return identity, nil
}
