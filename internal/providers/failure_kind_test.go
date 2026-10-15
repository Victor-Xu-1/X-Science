package providers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestProviderFailureKindUsesTypedHTTPAndClosedMachineCodes(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		want       FailureKind
	}{
		{"subscription", `{"error":{"code":"InvalidSubscription","type":"PRIVATE_PROVIDER_TYPE","param":"","message":"PRIVATE_ACCOUNT"}}`, 400, FailureSubscription},
		{"unknown_prose", `{"error":{"code":"UnknownFailure","message":"subscription expired; insufficient quota"}}`, 400, FailureUnavailable},
		{"authentication", `{"error":{"message":"PRIVATE_ACCOUNT"}}`, 401, FailureAuthentication},
		{"access", `{"error":{"message":"PRIVATE_ACCOUNT"}}`, 403, FailureAccessDenied},
		{"payment", `{}`, 402, FailureSubscription},
		{"malformed", `{"error":{"code":"InvalidSubscription"}} trailing`, 400, FailureUnavailable},
		{"temporary", `{"error":{"code":"InvalidSubscription"}}`, 503, FailureUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := fmt.Errorf("wrapped: %w", newResponseHTTPError(test.status, "private HTTP diagnostic", []byte(test.body)))
			kind, found := HTTPFailureKind(err)
			if !found || kind != test.want {
				t.Fatalf("kind=%q found=%t want=%q", kind, found, test.want)
			}
			if status, found := HTTPStatus(err); !found || status != test.status {
				t.Fatalf("transport status=%d found=%t", status, found)
			}
		})
	}
	if _, found := HTTPFailureKind(errors.New(`provider endpoint returned 400: {"error":{"code":"InvalidSubscription"}}`)); found {
		t.Fatal("untyped text became transport-owned evidence")
	}
	legacy := `provider endpoint returned 400: {"error":{"code":"InvalidSubscription","message":"PRIVATE_ACCOUNT"}}`
	status, kind, found := ParseLegacyHTTPFailure(legacy)
	if !found || status != http.StatusBadRequest || kind != FailureSubscription {
		t.Fatalf("legacy status=%d kind=%q found=%t", status, kind, found)
	}
	for _, invalid := range []string{"The model wrote: " + legacy, legacy + " trailing", strings.Repeat("x", 4097)} {
		if _, _, found := ParseLegacyHTTPFailure(invalid); found {
			t.Fatal("non-envelope or unbounded legacy diagnostic was accepted")
		}
	}
}
