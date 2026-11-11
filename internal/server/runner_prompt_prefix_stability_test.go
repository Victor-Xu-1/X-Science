package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/providers"
	"x-science/internal/skills"
)

type prefixRequestTransport func(*http.Request) (*http.Response, error)

func (transport prefixRequestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type prefixWireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type prefixWireRequest struct {
	Messages []prefixWireMessage `json:"messages"`
	Tools    json.RawMessage     `json:"tools"`
}

const prefixFixturePolicy = "Synthetic permission policy: existing parent approval and admitted tool boundaries remain required."
const prefixFixtureSkill = "Synthetic stable Skill instructions; no source files or user data are read."

// Exercise the production composition functions and real provider serializer.
// Only the source catalogue/history and HTTP transport are synthetic; this is
// not a full runner admission, authentication, or provider-cache measurement.
func capturePrefixWireRequest(t *testing.T, streaming bool, now, started time.Time, project, root, frame, policy string) []byte {
	t.Helper()
	session := sessionstore.Session{ID: frame, Project: &sessionstore.Project{ID: "unused-fallback-project"}}
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: transcriptstore.Stream{
		OwnerID: "private-owner-not-model-context", ProjectID: project, RootFrameID: root, FrameID: frame,
	}}}
	trusted := sessionRunnerTrustedRuntimeContext(session, run)
	messages, err := sessionEntriesToProviderMessages(
		appendSessionRunnerTrustedRuntimeContext("Synthetic agent profile.", trusted),
		[]eventjournal.Entry{{Message: map[string]any{"role": "user", "content": "请读取合成输入。"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	messages = appendRuntimeAgentPolicyContextMessage(messages, policy)
	messages = appendRuntimeSkillContextMessage(messages, prefixFixtureSkill)
	tools := []agentruntime.ToolSchema{{Name: "read_file", Description: "Synthetic admitted reader", Parameters: map[string]any{
		"type": "object", "required": []string{"path"}, "properties": map[string]any{"path": map[string]any{"type": "string"}},
	}}}
	mcp := runtimeMCPContext([]agentruntime.ToolSchema{{Name: "mcp__synthetic__lookup"}}, []skills.Skill{{
		Name: "synthetic-skill", Tools: []string{"mcp__synthetic__lookup"},
	}})
	messages = appendRuntimeMCPContextMessage(messages, mcp)
	messages = appendRuntimeTerminalPolicyContextMessage(messages, sessionRunnerTemporalGroundingContext(now, started))
	messages = appendSessionRunnerPublicCommunicationContract(messages)
	messages = appendRuntimeResponseLanguageContextMessage(messages, "zh")
	var wire []byte
	client, err := providers.NewRuntimeModelClient(providers.ModelProfile{
		Provider: providers.ProviderProfile{Protocol: providers.ProtocolOpenAICompatible, Endpoint: "https://synthetic.invalid/v1/chat/completions"},
		Model:    "synthetic-model",
	}, &http.Client{Transport: prefixRequestTransport(func(request *http.Request) (*http.Response, error) {
		var readErr error
		wire, readErr = io.ReadAll(request.Body)
		if readErr != nil {
			return nil, readErr
		}
		body := `{"choices":[{"message":{"role":"assistant","content":"accepted"},"finish_reason":"stop"}]}`
		contentType := "application/json"
		if streaming {
			contentType = "text/event-stream"
			body = "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"accepted\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}},
			Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := agentruntime.ModelRequest{Messages: agentRuntimeMessagesFromChat(messages), Tools: tools}
	if streaming {
		_, err = client.(agentruntime.StreamingModelClient).CompleteStream(t.Context(), request, func(agentruntime.ModelStreamEvent) error { return nil })
	} else {
		_, err = client.Complete(t.Context(), request)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) == 0 {
		t.Fatal("provider serializer did not reach the synthetic transport")
	}
	return wire
}

func decodePrefixWire(t *testing.T, wire []byte) prefixWireRequest {
	t.Helper()
	var payload prefixWireRequest
	if err := json.Unmarshal(wire, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestSessionRunnerProviderRequestPrefixKeepsStableContextsBeforeClock(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 600, time.UTC)
	times := []time.Time{started.Add(time.Hour + 123*time.Nanosecond), started.Add(time.Hour + time.Second + 987*time.Nanosecond)}
	for _, streaming := range []bool{false, true} {
		name := "nonstream"
		if streaming {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			var prefix, tools []byte
			for index, now := range times {
				wire := capturePrefixWireRequest(t, streaming, now, started, "synthetic-project", "synthetic-root", "synthetic-frame", prefixFixturePolicy)
				t.Logf("prefix_fixture_wire_json transport=%s sample=%d raw=%s", name, index, wire)
				payload := decodePrefixWire(t, wire)
				clock := bytes.Index(wire, []byte(now.Format(time.RFC3339Nano)))
				if clock < 0 || !bytes.Contains(wire[:clock], []byte(prefixFixtureSkill)) || !bytes.Contains(wire[:clock], []byte("mcp__synthetic__lookup")) {
					t.Fatal("fresh clock precedes the stable Skill/MCP prefix in real provider request JSON")
				}
				if index == 0 {
					prefix, tools = bytes.Clone(wire[:clock]), bytes.Clone(payload.Tools)
				} else if !bytes.Equal(prefix, wire[:clock]) || !bytes.Equal(tools, payload.Tools) {
					t.Fatal("changing only the trusted clock changed stable prefix or tool-schema bytes")
				}
				assertPrefixWireAuthority(t, payload, now, started, "synthetic-project", "synthetic-root", "synthetic-frame", prefixFixturePolicy)
			}
		})
	}
}

func assertPrefixWireAuthority(t *testing.T, payload prefixWireRequest, now, started time.Time, project, root, frame, policy string) {
	t.Helper()
	var all strings.Builder
	clockIndex, skillIndex, mcpIndex, policyIndex := -1, -1, -1, -1
	for index, message := range payload.Messages {
		all.WriteString(message.Content)
		all.WriteByte('\n')
		for marker, target := range map[string]*int{"- current_time_utc: ": &clockIndex, prefixFixtureSkill: &skillIndex, "X-Science MCP tool context:": &mcpIndex, policy: &policyIndex} {
			if strings.Contains(message.Content, marker) {
				*target = index
				if message.Role != "system" {
					t.Fatalf("trusted context %q changed role", marker)
				}
			}
		}
	}
	for _, value := range []string{
		"- project_id: " + project, "- root_frame_id: " + root, "- frame_id: " + frame,
		"- current_time_utc: " + now.Format(time.RFC3339Nano), "- current_date: " + now.Format(time.DateOnly),
		"- timezone: UTC", "- task_started_at: " + started.Format(time.RFC3339Nano),
		"- retrieval_as_of: " + now.Format(time.RFC3339Nano), policy,
	} {
		if strings.Count(all.String(), value+"\n") != 1 {
			t.Fatalf("runtime fact must occur exactly once: %q", value)
		}
	}
	if strings.Contains(all.String(), "private-owner-not-model-context") || strings.Contains(all.String(), "unused-fallback-project") {
		t.Fatal("request exposed private owner or stale fallback identity")
	}
	if clockIndex <= skillIndex || clockIndex <= mcpIndex || clockIndex <= policyIndex {
		t.Fatal("dynamic temporal authority must follow stable Skill/MCP/permission context")
	}
	leading := 0
	for leading < len(payload.Messages) && payload.Messages[leading].Role == "system" {
		leading++
	}
	communication := appendSessionRunnerPublicCommunicationContract(nil)[0].Content
	if clockIndex >= leading-2 || payload.Messages[leading-2].Content != communication || payload.Messages[leading-1].Content != simplifiedChineseResponseLanguageContext {
		t.Fatal("temporal layout changed the final communication/language system guards")
	}
	if leading != len(payload.Messages)-1 || payload.Messages[leading].Role != "user" {
		t.Fatal("provider role/history order changed")
	}
}

func TestSessionRunnerProviderRequestKeepsFreshScopeAndTaskStart(t *testing.T) {
	now := time.Date(2026, 2, 3, 4, 5, 6, 789, time.UTC)
	for _, test := range []struct {
		name, project, root, frame, policy string
		started                            time.Time
	}{
		{"durable-start", "project-a", "root-a", "frame-a", prefixFixturePolicy, now.Add(-time.Hour)},
		{"different-scope", "project-b", "root-b", "frame-b", "Synthetic permission policy: read_only; mutation remains forbidden.", now.Add(-time.Minute)},
		{"missing-start-existing-fallback", "project-a", "root-a", "frame-a", prefixFixturePolicy, time.Time{}},
		{"future-start-existing-fallback", "project-a", "root-a", "frame-a", prefixFixturePolicy, now.Add(time.Hour)},
	} {
		t.Run(test.name, func(t *testing.T) {
			started := test.started
			if started.IsZero() || started.After(now) {
				started = now
			}
			wire := capturePrefixWireRequest(t, false, now, test.started, test.project, test.root, test.frame, test.policy)
			assertPrefixWireAuthority(t, decodePrefixWire(t, wire), now, started, test.project, test.root, test.frame, test.policy)
		})
	}
	if !reflect.DeepEqual(sessionRunnerTaskStartedAt(sessionstore.Session{CreatedAt: now}, nil), now) {
		t.Fatal("existing durable task-start fallback changed")
	}
}
