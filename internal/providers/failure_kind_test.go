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

func TestProviderPermanentAccount429IsNotRetryableWithoutQuotaProse(t *testing.T) {
	for _, code := range []string{
		"InvalidSubscription", "subscription_expired", "billing_not_active", "billing_hard_limit_reached",
		"invalid_api_key", "authentication_error", "account_deactivated", "permission_denied", "access_denied",
	} {
		t.Run(code, func(t *testing.T) {
			body := []byte(`{"error":{"code":"` + code + `","message":"repair account status"}}`)
			if providerHTTPStatusRetryable(http.StatusTooManyRequests, body) {
				t.Fatal("recognized permanent account rejection was treated as transient throttling")
			}
		})
	}
	for _, test := range []struct {
		name, body string
		status     int
		want       bool
	}{
		{"unknown_429", `{"error":{"code":"UnknownFailure","message":"try later"}}`, 429, true},
		{"throttling", `{"error":{"code":"rate_limit_exceeded","message":"too many requests"}}`, 429, true},
		{"server_failure", `{"error":{"code":"InvalidSubscription","message":"repair account status"}}`, 503, true},
		{"old_quota", `{"error":{"message":"quota exhausted"}}`, 429, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := providerHTTPStatusRetryable(test.status, []byte(test.body)); got != test.want {
				t.Fatalf("retryable=%t want=%t", got, test.want)
			}
		})
	}
}
