package providers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// FailureKind is a closed presentation category, never retry or authorization
// authority. Provider prose is deliberately excluded from its classification.
type FailureKind string

const (
	FailureUnavailable    FailureKind = "unavailable"
	FailureSubscription   FailureKind = "subscription"
	FailureAuthentication FailureKind = "authentication"
	FailureAccessDenied   FailureKind = "access_denied"
)

// HTTPFailureKind consumes the same transport-owned error as HTTPStatus.
func HTTPFailureKind(err error) (FailureKind, bool) {
	var response *responseHTTPError
	if !errors.As(err, &response) {
		return "", false
	}
	if response.failureKind == "" {
		return FailureUnavailable, true
	}
	return response.failureKind, true
}

func providerHTTPFailureKind(status int, body []byte) FailureKind {
	switch status {
	case http.StatusUnauthorized:
		return FailureAuthentication
	case http.StatusForbidden:
		return FailureAccessDenied
	case http.StatusPaymentRequired:
		return FailureSubscription
	}
	if status != http.StatusBadRequest && status != http.StatusTooManyRequests {
		return FailureUnavailable
	}
	// Bounded structured machine codes only. An unknown code whose message says
	// "subscription expired" must not become billing/configuration authority.
	if len(body) > 4096 {
		return FailureUnavailable
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return FailureUnavailable
	}
	switch strings.ToLower(envelope.Error.Code) {
	case "invalidsubscription", "subscription_expired", "billing_not_active",
		"insufficient_quota", "billing_hard_limit_reached":
		return FailureSubscription
	case "invalid_api_key", "authentication_error":
		return FailureAuthentication
	case "account_deactivated", "permission_denied", "access_denied":
		return FailureAccessDenied
	default:
		return FailureUnavailable
	}
}

// ParseLegacyHTTPFailure is limited to private historical HTTP diagnostics
// on an already-authoritative provider interruption. It is not a model-message
// parser, and it never changes a stored event or reconstructs execution rights.
func ParseLegacyHTTPFailure(detail string) (int, FailureKind, bool) {
	if len(detail) > 4096 {
		return 0, "", false
	}
	for _, prefix := range []string{"provider endpoint returned ", "model provider returned HTTP "} {
		if !strings.HasPrefix(detail, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(detail, prefix)
		if len(remainder) < 5 || remainder[3:5] != ": " {
			return 0, "", false
		}
		status, err := strconv.Atoi(remainder[:3])
		body := []byte(remainder[5:])
		if err != nil || status < 400 || status > 599 || !json.Valid(body) {
			return 0, "", false
		}
		return status, providerHTTPFailureKind(status, body), true
	}
	return 0, "", false
}
