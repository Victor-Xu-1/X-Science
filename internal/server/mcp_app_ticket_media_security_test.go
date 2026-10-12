package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"x-science/internal/observability"
)

func TestMCPAppTicketMarkupStaysJSONThroughObservedHTTPWriter(t *testing.T) {
	const markup = `<script>globalThis.untrustedMarkupExecuted=true</script>`
	handler := observability.NewHTTPRegistry().Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]string
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		writeMCPAppTicketJSON(w, http.StatusCreated, input)
	}))
	body, err := json.Marshal(map[string]string{"markup": markup})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/ticket", strings.NewReader(string(body))))
	if response.Code != http.StatusCreated || response.Header().Get("Content-Type") != "application/json" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unexpected executable media response: %d %v", response.Code, response.Header())
	}
	var decoded map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || decoded["markup"] != markup {
		t.Fatalf("JSON user data changed: %#v err=%v", decoded, err)
	}
}
