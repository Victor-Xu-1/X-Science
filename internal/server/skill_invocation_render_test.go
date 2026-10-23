package server

import (
	"fmt"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/skills"
)

func TestSkillInvocationRenderedSourceRequiresFullBody(t *testing.T) {
	const name, body = "source-envelope-fixture", "Before< scope alpha >After"
	full := agentRuntimeSkillModelResult(map[string]any{
		"skill":  map[string]any{"name": name, "description": "Original synthetic contract.", "body_hash": "diagnostic-only"},
		"prompt": body, "data": map[string]any{"loaded": true, "executed": false},
	})
	for _, source := range []any{full, body, map[string]any{"prompt": body}} {
		prompt, valid := runtimeSkillReplayRenderedPrompt(source, name)
		if !valid || prompt != body {
			t.Fatal("full original producer body did not retain its exact raw argument rendering")
		}
	}
	for _, source := range []any{
		agentRuntimeLegacySkillModelResult(full),
		strings.SplitN(full.(string), "\n", 2)[0],
		strings.TrimSuffix(full.(string), agentRuntimeSkillActivePostlude),
		strings.Replace(full.(string), name, "foreign-source-fixture", 1),
		map[string]any{"status": "already_loaded", "reused": true},
		map[string]any{"preview": body}, map[string]any{"prompt": 2},
	} {
		if _, valid := runtimeSkillReplayRenderedPrompt(source, name); valid {
			t.Fatalf("identity-only, foreign or incomplete result impersonated a full source: %T", source)
		}
	}
}

func TestSkillInvocationRenderingKeepsLiteralNamedArguments(t *testing.T) {
	const raw = "chemical-$1-${label}-$0-$ARGUMENTS"
	for _, token := range []string{"$scope", "$ARGUMENTS[0]", "$0", "$ARGUMENTS"} {
		t.Run(token, func(t *testing.T) {
			item := skills.Skill{Name: "literal-argument-fixture", Path: "builtin:literal-argument-fixture",
				Body: "Before<" + token + ">After", Arguments: []string{"scope"}}
			_, prompt, err := renderRuntimeSkillInvocation([]skills.Skill{item}, item.Name, raw, "")
			if err != nil || !strings.Contains(prompt, "Before<"+raw+">After") {
				t.Fatalf("argument data was interpreted as another template: prompt=%q err=%v", prompt, err)
			}
		})
	}
}

func TestSkillArgumentTemplatePreservesPlaceholderPrecedence(t *testing.T) {
	for _, tc := range []struct {
		template, args string
		names          []string
		want           string
	}{
		{"<$scope>|<$ARGUMENTS[1]>|<$1>|<$ARGUMENTS>", "'first scope' second", []string{"scope"}, "<first scope>|<second>|<second>|<'first scope' second>"},
		{"<$scope>|<$scope-long>|<$scopeX>", "first second", []string{"scope", "scope-long"}, "<first>|<first-long>|<$scopeX>"},
		{"<$ARGUMENTS[0]>|<$ARGUMENTSX>", "first", []string{"ARGUMENTS"}, "<first[0]>|<firstX>"},
		{"<$2>|<$ARGUMENTS[99]>", "first", nil, "<>|<>"},
		{"<$scope>", "first second", []string{"scope", "scope"}, "<first>"},
		{"no tokens", " raw scope ", nil, "no tokens\n\nARGUMENTS:  raw scope "},
		{"no tokens", "  ", nil, "no tokens"},
	} {
		if got := substituteSkillArguments(tc.template, tc.args, tc.names); got != tc.want {
			t.Fatalf("template=%q got=%q want=%q", tc.template, got, tc.want)
		}
	}
}

func TestSkillArgumentTemplateHandlesDenseTokens(t *testing.T) {
	// Moderate legal input only: no GiB allocation or machine-pressure probe.
	names := make([]string, 512)
	for index := range names {
		names[index] = fmt.Sprintf("scope_%d", index)
	}
	const raw = "literal-$0-$ARGUMENTS"
	if got := substituteSkillArguments(strings.Repeat("<$0>", 4096), raw, names); got != strings.Repeat("<"+raw+">", 4096) {
		t.Fatal("dense tokens or a large declared-name set changed literal rendering")
	}
}

func TestSkillInvocationFilterUsesCapturedRenderingOwner(t *testing.T) {
	schemas := []agentruntime.ToolSchema{
		{Name: "mcp__fixture__method_00", Description: "Unrelated method", Parameters: map[string]any{"type": "object"}},
		{Name: "mcp__fixture__method_07", Description: "Requested method", Parameters: map[string]any{"type": "object"}},
	}
	body, _ := runtimeMCPConnectorSkillBody("fixture", schemas)
	item := skills.Skill{Name: "mcp-fixture", Path: "builtin:mcp-fixture", Body: body}
	effective, prompt, err := renderRuntimeSkillInvocation([]skills.Skill{item}, item.Name, " scope ", " method 07 ")
	filtered, found := runtimeMCPConnectorFilterRenderedBody(body, "method 07")
	if !found {
		t.Fatal("real connector body did not support the actual filter owner")
	}
	expected := item
	expected.Body = filtered
	want, renderErr := renderComposedSkillPrompt([]skills.Skill{expected}, item.Name, " scope ")
	if err != nil || renderErr != nil || prompt != want || len(effective) != 1 || effective[0].Body != filtered || item.Body != body {
		t.Fatalf("live/replay filtering diverged or mutated the captured contract: %v %v", err, renderErr)
	}
}

func TestSkillReplaySharedContextUsesCanonicalAcknowledgement(t *testing.T) {
	root := func(sequence int64, members ...skills.Skill) runtimeSkillReplayContextRoot {
		return runtimeSkillReplayContextRoot{publication: sequence, members: members}
	}
	member := func(name, body string) skills.Skill { return skills.Skill{Name: name, Body: body} }
	for _, tc := range []struct {
		name   string
		proofs map[string]runtimeSkillReplayContextRoot
		want   []string
	}{
		{"latest-shared-body", map[string]runtimeSkillReplayContextRoot{
			"older": root(1, member("shared", "empty")), "newer": root(2, member("shared", "scoped")),
			"independent": root(3, member("other", "original")),
		}, []string{"newer", "independent"}},
		{"rejected-root-does-not-shadow", map[string]runtimeSkillReplayContextRoot{
			"latest": root(3, member("shared", "current")), "conflicting": root(2, member("shared", "old"), member("other", "unused")),
			"independent": root(1, member("other", "original")),
		}, []string{"latest", "independent"}},
		{"ambiguous-tie", map[string]runtimeSkillReplayContextRoot{
			"left": root(4, member("shared", "left")), "right": root(4, member("shared", "right")),
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accepted := compatibleRuntimeSkillReplayContexts(tc.proofs)
			if len(accepted) != len(tc.want) {
				t.Fatalf("canonical context selection kept %d roots, want %d", len(accepted), len(tc.want))
			}
			for _, name := range tc.want {
				if _, found := accepted[name]; !found {
					t.Fatalf("canonical context selection lost %s", name)
				}
			}
		})
	}
}
