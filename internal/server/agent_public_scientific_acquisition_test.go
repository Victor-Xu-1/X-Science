package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/tools/securefetch"
	"x-science/internal/tools/webfetch"
	"x-science/internal/tools/websearch"
)

// The registered web_fetch and the streaming downloader both use the real
// existing executors against one controlled TLS server, not a source receipt
// fabricated by the test. Public host/DNS protections have separate controls.
func scientificAcquisitionGatewayFixture(t *testing.T, includeFetch bool, onRequest ...func(*http.Request)) (*agentSaveArtifactsFixture, serverAgentRuntimeToolGateway, agentruntime.ToolCall, context.Context, *atomic.Int32, string) {
	t.Helper()
	fixture := newAgentSaveArtifactsFixture(t)
	content := strings.Repeat("sample,value\nA,1\n", 1024)
	requests := &atomic.Int32{}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		for _, observe := range onRequest {
			observe(r)
		}
		if r.Host != "source.example.org" || r.URL.RequestURI() != "/records.csv?release=one&part=1" {
			t.Errorf("changed exact source request: %s %s", r.Host, r.URL.RequestURI())
		}
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		_, _ = io.WriteString(w, content)
	}))
	t.Cleanup(upstream.Close)
	transport := upstream.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.InsecureSkipVerify = true // Virtual public host on a controlled local TLS fixture only.
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	fixture.server.tools.SetWebOptions(webfetch.Options{
		MaxAttempts: 1,
		ClientForURL: func(context.Context, string) (*http.Client, error) {
			clone := *client
			return &clone, nil
		},
	}, websearch.Options{})
	fixture.server.publicScientificFiles = securefetch.New(securefetch.Options{
		HTTPClient: client, TestOnlyAllowCustomTransport: true, Resolver: scientificRedirectTestResolver{},
	})
	fixture.server.publicScientificDownloadSlots = make(chan struct{}, 2)
	input := map[string]any{
		"url":      "https://source.example.org/records.csv?release=one&part=1",
		"filename": "records.csv", "human_description": "Downloading a synthetic source",
	}
	call := agentruntime.ToolCall{ID: "acquisition-parent", Name: "download_public_scientific_file", Arguments: mustMarshalRawMessage(input)}
	run := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID,
		Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}}
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	allowed := []string{call.Name}
	if includeFetch {
		allowed = append(allowed, "web_fetch")
	}
	schemas := []agentruntime.ToolSchema{agentPublicScientificFileToolSchema()}
	if includeFetch {
		tool, found := fixture.server.registeredTool("web_fetch")
		if !found {
			t.Fatal("canonical web_fetch is not registered")
		}
		schemas = append(schemas, agentruntime.ToolSchema{Name: tool.Name, Description: tool.Description, Parameters: chatToolParameters(tool)})
	}
	gateway := serverAgentRuntimeToolGateway{server: fixture.server, taskRun: run,
		kernel: fixture.identity, sessionID: fixture.stream.SessionID, allowedTools: allowed,
		toolSchemas: schemas, toolValidators: agentRuntimeToolValidators(schemas), hasToolSnapshot: true}
	return fixture, gateway, call, ctx, requests, content
}

func declareScientificFixtureTool(t *testing.T, fixture *agentSaveArtifactsFixture, gateway serverAgentRuntimeToolGateway, call agentruntime.ToolCall, description string) {
	t.Helper()
	options := SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}
	if err := fixture.server.checkpointChatModelToolCalls(options, gateway.taskRun, []agentruntime.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.server.checkpointChatTool(options, gateway.taskRun, "running", description, call.ID, "start", map[string]any{
		"toolName": call.Name, "toolInput": json.RawMessage(call.Arguments),
	}); err != nil {
		t.Fatal(err)
	}
}

func scientificAcquisitionFixture(t *testing.T, includeFetch bool, onRequest ...func(*http.Request)) (*agentSaveArtifactsFixture, serverAgentRuntimeToolGateway, agentruntime.ToolCall, context.Context, *atomic.Int32, string) {
	t.Helper()
	fixture, gateway, call, ctx, requests, content := scientificAcquisitionGatewayFixture(t, includeFetch, onRequest...)
	declareScientificFixtureTool(t, fixture, gateway, call, "Starting the download")
	return fixture, gateway, call, ctx, requests, content
}

func TestAgentPublicScientificNativeSourceAcquisition(t *testing.T) {
	t.Run("missing_source_is_internal_preparation", func(t *testing.T) {
		_, gateway, call, ctx, requests, _ := scientificAcquisitionFixture(t, true)
		if diagnostic := gateway.toolCallPreflightDiagnostic(ctx, call); diagnostic != "" {
			t.Fatalf("missing prior source still demands another model tool call: %s", diagnostic)
		}
		if requests.Load() != 0 {
			t.Fatal("model preflight performed network acquisition")
		}
	})
	t.Run("one_parent_transfer_and_replay", func(t *testing.T) {
		fixture, gateway, call, ctx, requests, content := scientificAcquisitionFixture(t, true)
		result, err := gateway.Execute(ctx, call)
		value := mapValue(result.Value)
		if err != nil || value["ok"] != true {
			_, events := scientificAcquisitionEvents(t, fixture)
			for _, event := range events {
				var phase nativeScientificAcquisition
				_ = json.Unmarshal(event.Event.PayloadJSON, &phase)
				if phase.Schema == agentPublicScientificAcquisitionSchema {
					t.Logf("source phase: %s", event.Event.PayloadJSON)
				}
			}
			t.Fatalf("single admitted parent did not acquire its source and complete download: %#v err=%v requests=%d", value, err, requests.Load())
		}
		download := mapValue(value["download"])
		artifacts := agentSaveArtifactResults(t, value)
		sourceID := stringValue(download["source_tool_call_id"])
		if sourceID == "" || sourceID == call.ID || download["source_url"] != "https://source.example.org/records.csv?release=one&part=1" ||
			download["sha256"] != scientificSourceDigest([]byte(content)) || len(artifacts) != 1 || requests.Load() != 2 {
			t.Fatalf("source identity or bounded acquisition/transfer changed: %#v requests=%d", download, requests.Load())
		}
		stored, err := os.ReadFile(filepath.Join(fixture.projectPath, "records.csv"))
		if err != nil || string(stored) != content {
			t.Fatalf("full source bytes changed: bytes=%d err=%v", len(stored), err)
		}
		replayed, err := gateway.Execute(ctx, call)
		if err != nil || mapValue(replayed.Value)["ok"] != true || requests.Load() != 2 ||
			mapValue(mapValue(replayed.Value)["download"])["source_tool_call_id"] != sourceID {
			t.Fatalf("same parent replay repeated acquisition or lost its source: %#v err=%v requests=%d", replayed.Value, err, requests.Load())
		}
		replayedArtifacts := agentSaveArtifactResults(t, mapValue(replayed.Value))
		if len(replayedArtifacts) != 1 || replayedArtifacts[0]["version_id"] != artifacts[0]["version_id"] {
			t.Fatalf("same parent replay created another artifact version: first=%#v replayed=%#v", artifacts, replayedArtifacts)
		}
	})
	t.Run("missing_fetch_snapshot_never_expands", func(t *testing.T) {
		fixture, gateway, call, ctx, requests, _ := scientificAcquisitionFixture(t, false)
		result, err := gateway.Execute(ctx, call)
		if err == nil && mapValue(result.Value)["ok"] == true || requests.Load() != 0 {
			t.Fatalf("missing inherited web_fetch authority was expanded: %#v err=%v requests=%d", result.Value, err, requests.Load())
		}
		if _, err := os.Stat(filepath.Join(fixture.projectPath, "records.csv")); !os.IsNotExist(err) {
			t.Fatalf("blocked acquisition wrote a file: %v", err)
		}
	})
}
