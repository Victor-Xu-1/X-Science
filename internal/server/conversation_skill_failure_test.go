package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/providers"
)

func TestConversationSkillAuthoringFailureDiagnosticIsMetadataOnly(t *testing.T) {
	record := providers.AuditRecord{ProviderID: "synthetic-private-marker", Model: "synthetic-private-marker",
		Endpoint: "https://private.invalid/path", Error: "synthetic-private-marker",
		HTTPStatus: 200, DurationMs: 42, RequestedOutputTokens: 16384, CompletionTokens: 16384,
		OutputTokenLimited: true}
	event := conversationSkillModelDiagnostic(record)
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private") || event.Code != skillAuthoringOutputLimit ||
		event.DurationMS != 42 || event.RequestedOutputTokens != 16384 || event.CompletionTokens != 16384 {
		t.Fatalf("diagnostic projection did not retain safe evidence: %s", encoded)
	}
	for _, operation := range []string{"unsafe\noperation", "../private", strings.Repeat("x", 129)} {
		if withConversationSkillOperation(context.Background(), operation).Value(conversationSkillOperationContextKey{}) != nil {
			t.Fatal("unsafe operation identifier entered the log context")
		}
	}
	for _, test := range []struct {
		cause error
		code  conversationSkillFailureCode
	}{
		{context.DeadlineExceeded, skillAuthoringTimeout}, {context.Canceled, skillAuthoringCancelled},
	} {
		failure := conversationSkillFailure(test.cause, skillAuthoringProviderUnavailable)
		if !errors.Is(failure, test.cause) || conversationSkillErrorCode(failure) != test.code {
			t.Fatal("safe classification lost cancellation/deadline identity")
		}
	}
}

func TestConversationSkillAuthoringFailureBudgetUsesModelAuthority(t *testing.T) {
	for _, budget := range []int{0, 1024, 16384} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			fixture := newConversationSkillAuthoringFixture(t, []conversationSkillAuthoringReply{
				conversationSkillToolReplyForTest(conversationSkillDraftArgumentsForTest(t, nil)),
			})
			if budget > 0 {
				provider, found, err := fixture.server.workspaceStore.GetModelProvider("local", "skill-authoring-provider")
				if err != nil || !found {
					t.Fatalf("fixture provider missing: %v", err)
				}
				if _, err := fixture.server.workspaceStore.UpsertModelProvider(workspace.ModelProviderInput{
					ID: provider.ID, UserID: provider.UserID, Name: provider.Name, Type: provider.Type,
					BaseURL: provider.BaseURL, Model: provider.Model, MaxTokens: &budget, MaxTokensSet: true,
				}); err != nil {
					t.Fatal(err)
				}
			}
			response := fixture.save(context.Background())
			if response.Code != http.StatusCreated {
				t.Fatalf("save status=%d", response.Code)
			}
			requests := fixture.capturedRequests()
			if len(requests) != 1 {
				t.Fatalf("requests=%d want1", len(requests))
			}
			if budget == 0 {
				if _, found := requests[0]["max_tokens"]; found {
					t.Fatal("authoring invented an output budget instead of retaining provider default")
				}
			} else {
				var actual int
				if err := json.Unmarshal(requests[0]["max_tokens"], &actual); err != nil || actual != budget {
					t.Fatalf("model budget=%d want=%d err=%v", actual, budget, err)
				}
			}
		})
	}
}

func TestConversationSkillAuthoringFailureCodes(t *testing.T) {
	invalid := conversationSkillToolReplyForTest(conversationSkillDraftArgumentsForTest(t, func(fields map[string]any) {
		delete(fields, "kernel_py")
	}))
	cases := []struct {
		name    string
		replies []conversationSkillAuthoringReply
		code    string
	}{
		{"invalid_draft", []conversationSkillAuthoringReply{invalid, invalid}, "skill_authoring_invalid_draft"},
		{"output_limit", []conversationSkillAuthoringReply{{stopReason: "length", message: map[string]any{"role": "assistant", "content": "partial draft"}}}, "skill_authoring_output_limit"},
		{"refusal", []conversationSkillAuthoringReply{{stopReason: "content_filter", message: map[string]any{"role": "assistant", "content": "not a draft"}}}, "skill_authoring_refused"},
		{"provider_auth", []conversationSkillAuthoringReply{{status: http.StatusForbidden}}, "skill_authoring_provider_auth"},
		{"rate_limited", []conversationSkillAuthoringReply{{status: http.StatusTooManyRequests}}, "skill_authoring_rate_limited"},
		{"provider_failure", []conversationSkillAuthoringReply{{status: http.StatusServiceUnavailable}}, "skill_authoring_provider_unavailable"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newConversationSkillAuthoringFixture(t, test.replies)
			response := fixture.save(context.Background())
			var payload map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusBadGateway || payload["code"] != test.code {
				t.Fatalf("safe failure status=%d code=%v want=%s", response.Code, payload["code"], test.code)
			}
			if len(fixture.capturedRequests()) != len(test.replies) {
				t.Fatalf("failure added an authoring retry: got=%d want=%d", len(fixture.capturedRequests()), len(test.replies))
			}
			fixture.assertNoInstallation(t)
		})
	}
}

func TestConversationSkillAuthoringFailureHidesRawTransport(t *testing.T) {
	fixture := newConversationSkillAuthoringFixture(t, nil)
	fixture.server.httpClient = &http.Client{Transport: conversationSkillAuthoringTransportFunc(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("synthetic-diagnostic-secret-marker at https://private.invalid/path")
	})}
	response := fixture.save(context.Background())
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusBadGateway || payload["code"] != "skill_authoring_provider_unavailable" ||
		strings.Contains(response.Body.String(), "synthetic-diagnostic-secret-marker") ||
		strings.Contains(response.Body.String(), "private.invalid") {
		t.Fatalf("transport failure did not preserve a safe diagnostic contract: status=%d code=%v rawBodyLeaked=%t", response.Code, payload["code"], strings.Contains(response.Body.String(), "private.invalid"))
	}
	fixture.assertNoInstallation(t)
}
