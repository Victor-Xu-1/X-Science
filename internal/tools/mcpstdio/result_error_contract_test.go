package mcpstdio

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type resultErrorContractSession struct {
	readOnlyHint *bool
	isError      bool
	modern       bool
	methods      []string
}

const resultErrorContractPayload = `{"found":false,"code":"not_found","error":"selected record not found"}`

func (s *resultErrorContractSession) request(_ context.Context, _ int, method string, _ any) (json.RawMessage, error) {
	s.methods = append(s.methods, method)
	switch method {
	case "server/discover":
		if s.modern {
			return mustRawJSON(map[string]any{"supportedVersions": []string{latestProtocolVersion}}), nil
		}
		return nil, &mcpRPCError{Method: method, Code: -32601, Message: "method not found"}
	case "initialize":
		return mustRawJSON(map[string]any{"protocolVersion": latestLegacyProtocolVersion}), nil
	case "tools/list":
		tool := map[string]any{"name": "selected_record", "inputSchema": map[string]any{"type": "object"}}
		if s.readOnlyHint != nil {
			tool["annotations"] = map[string]any{"readOnlyHint": *s.readOnlyHint}
		}
		// An unrelated read-only tool must not lend its hint to this result.
		decoy := map[string]any{"name": "unrelated_lookup", "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true}}
		return mustRawJSON(map[string]any{"tools": []any{decoy, tool}}), nil
	case "tools/call":
		return mustRawJSON(map[string]any{"isError": s.isError, "content": []any{map[string]any{"type": "text", "text": resultErrorContractPayload}}}), nil
	default:
		return nil, errors.New("unexpected method " + method)
	}
}

func (s *resultErrorContractSession) notify(_ context.Context, method string, _ any) error {
	s.methods = append(s.methods, method)
	return nil
}

func TestInspectAndCallLocalSessionBindsNotFoundCompatibilityToReadOnlyHint(t *testing.T) {
	readOnly, mutating := true, false
	for _, protocol := range []string{"legacy", "modern"} {
		for _, tc := range []struct {
			name         string
			readOnlyHint *bool
			isError      bool
			wantError    bool
		}{
			{name: "read_only_lookup_miss", readOnlyHint: &readOnly, isError: true},
			{name: "mutation_not_found", readOnlyHint: &mutating, isError: true, wantError: true},
			{name: "unannotated_not_found", isError: true, wantError: true},
			{name: "successful_mutation_data", readOnlyHint: &mutating},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				session := &resultErrorContractSession{readOnlyHint: tc.readOnlyHint, isError: tc.isError, modern: protocol == "modern"}
				inspected := 0
				output, err := inspectAndCallLocalSession(context.Background(), session, "fixture", "selected_record", map[string]any{}, func(tool ToolProjection) error {
					inspected++
					if tool.ToolName != "selected_record" || tool.ReadOnlyHint != (tc.readOnlyHint != nil && *tc.readOnlyHint) {
						t.Errorf("live tool=%q readOnlyHint=%v", tool.ToolName, tool.ReadOnlyHint)
					}
					return validateToolInput(tool.InputSchema, map[string]any{})
				})
				wantMethods := "server/discover,initialize,notifications/initialized,tools/list,tools/call"
				if session.modern {
					wantMethods = "server/discover,tools/list,tools/call"
				}
				if inspected != 1 || strings.Join(session.methods, ",") != wantMethods {
					t.Fatalf("inspection count=%d methods=%v", inspected, session.methods)
				}
				if tc.wantError {
					if err == nil || output != "" || !strings.Contains(err.Error(), "selected record not found") {
						t.Fatalf("mutation or unclassified protocol error became data: output=%q err=%v", output, err)
					}
					return
				}
				if err != nil || output != resultErrorContractPayload {
					t.Fatalf("expected data = %q, got output=%q err=%v", resultErrorContractPayload, output, err)
				}
			})
		}
	}
}

func TestInspectAndCallLocalSessionKeepsInspectionFailureBeforeCall(t *testing.T) {
	readOnly := true
	for _, modern := range []bool{false, true} {
		session := &resultErrorContractSession{readOnlyHint: &readOnly, isError: true, modern: modern}
		denied := errors.New("fixture inspection denied")
		output, err := inspectAndCallLocalSession(context.Background(), session, "fixture", "selected_record", map[string]any{}, func(ToolProjection) error {
			return denied
		})
		if !errors.Is(err, denied) || output != "" || strings.Contains(strings.Join(session.methods, ","), "tools/call") {
			t.Fatalf("modern=%v output=%q err=%v methods=%v", modern, output, err, session.methods)
		}
	}
}

// Exercise the public call boundary and real JSON-RPC transport. An expected
// read-only lookup miss can remain data, but a mutating tool's error must not
// become a successful call just because it has the same result fields.
func TestCallToolForServerBindsNotFoundCompatibilityToReadOnlyHint(t *testing.T) {
	for _, protocol := range []string{"legacy", "modern"} {
		for _, tc := range []struct {
			name        string
			annotations map[string]any
			isError     bool
			wantError   bool
		}{
			{name: "read_only_lookup_miss", annotations: map[string]any{"readOnlyHint": true}, isError: true},
			{name: "mutation_not_found", annotations: map[string]any{"readOnlyHint": false}, isError: true, wantError: true},
			{name: "unannotated_not_found", isError: true, wantError: true},
			{name: "successful_mutation_data", annotations: map[string]any{"readOnlyHint": false}},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				const payload = resultErrorContractPayload
				var calls atomic.Int32
				fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request rpcMessage
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Errorf("decode request: %v", err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					var result any
					switch request.Method {
					case "server/discover":
						if protocol == "legacy" {
							w.WriteHeader(http.StatusBadRequest)
							_ = json.NewEncoder(w).Encode(map[string]any{
								"jsonrpc": "2.0", "id": request.ID,
								"error": map[string]any{"code": -32601, "message": "method not found"},
							})
							return
						}
						result = map[string]any{"supportedVersions": []string{latestProtocolVersion}, "capabilities": map[string]any{}, "resultType": "complete"}
					case "initialize":
						w.Header().Set("Mcp-Session-Id", "result-contract-session")
						result = map[string]any{"protocolVersion": latestLegacyProtocolVersion, "capabilities": map[string]any{}}
					case "notifications/initialized":
						w.WriteHeader(http.StatusAccepted)
						return
					case "tools/list":
						tool := map[string]any{"name": "selected_record", "inputSchema": map[string]any{"type": "object"}}
						if tc.annotations != nil {
							tool["annotations"] = tc.annotations
						}
						result = map[string]any{"tools": []any{tool}, "resultType": "complete"}
					case "tools/call":
						calls.Add(1)
						result = map[string]any{"isError": tc.isError, "content": []any{map[string]any{"type": "text", "text": payload}}, "resultType": "complete"}
					default:
						t.Errorf("unexpected method %q", request.Method)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
				}))
				defer fixture.Close()
				endpoint, client := oauthTLSTestClient(t, fixture)
				ctx := withRemoteSecurityFixtureClient(context.Background(), client)
				output, err := CallToolForServer(ctx, t.TempDir(), "fixture", "selected_record", map[string]any{}, ServerConfig{
					Type: "http", URL: endpoint,
				})
				if calls.Load() != 1 {
					t.Fatalf("tools/call count = %d, want one real call; output=%q err=%v", calls.Load(), output, err)
				}
				if tc.wantError {
					if err == nil || output != "" || !strings.Contains(err.Error(), "selected record not found") {
						t.Fatalf("mutation or unclassified protocol error became data: output=%q err=%v", output, err)
					}
					return
				}
				if err != nil || output != payload {
					t.Fatalf("expected data = %q, got output=%q err=%v", payload, output, err)
				}
			})
		}
	}
}
