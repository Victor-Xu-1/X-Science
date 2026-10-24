package memoryextract

import (
	"errors"
	"testing"
)

func TestWorkspaceForkedResponseParsesFirstBalancedJSONObject(t *testing.T) {
	input := "```json\nmodel preface {\"append\":[{\"text\":\"brace } and \\\"quote\\\"\",\"evidence\":\"stated\"}],\"nested\":{\"ok\":true}} ignored {\"remove\":[\"later\"]}\n```"
	got, err := ParseForkedResponse(input)
	if err != nil {
		t.Fatal(err)
	}
	appendRows, ok := got["append"].([]any)
	if !ok || len(appendRows) != 1 {
		t.Fatalf("forked response = %#v", got)
	}
	if nested, ok := got["nested"].(map[string]any); !ok || nested["ok"] != true {
		t.Fatalf("nested response = %#v", got["nested"])
	}
	if _, exists := got["remove"]; exists {
		t.Fatalf("parser consumed a later JSON object: %#v", got)
	}
}

func TestWorkspaceForkedResponseRejectsMalformedObject(t *testing.T) {
	for name, input := range map[string]string{
		"no object":            "nothing durable",
		"unterminated":         `before {"append": [`,
		"invalid":              `before {append: []} after`,
		"json null":            `before {"append": null,} after`,
		"empty text":           "",
		"scalar null":          "null",
		"empty array":          "[]",
		"invalid first object": "before {bad} after {}",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := ParseForkedResponse(input); got != nil || !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("invalid response was acknowledged: result=%#v err=%v", got, err)
			}
		})
	}
}

func TestWorkspaceForkedResponseAcceptsExplicitEmptyObject(t *testing.T) {
	for _, input := range []string{"{}", "before {} after", "```json\n{}\n```", `{"append":[],"replace":[],"remove":[]}`} {
		got, err := ParseForkedResponse(input)
		if err != nil || got == nil || len(ParseEmitted(got, nil, 10).Append) != 0 {
			t.Fatalf("explicit valid empty acknowledgement was rejected: result=%#v err=%v", got, err)
		}
	}
}
