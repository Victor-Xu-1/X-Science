package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"synon-go/internal/agentruntime"
	"synon-go/internal/tools/articlefulltext"
	toolregistry "synon-go/internal/tools/registry"
)

func TestAgentRuntimeRealSourceAvailabilitySurvivesTypedGateway(t *testing.T) {
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/PMC900/fullTextXML":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<article>Actual fixture full text.</article>`))
		case "/PMC503/fullTextXML":
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		case "/search":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"resultList":{"result":[{"doi":"10.1000/abstract","title":"Source record","abstractText":"Measured methods and results, with no open full text."}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer source.Close()
	options, _ := articlefulltextPinnedTestOptions(t, source)
	srv := New(Options{FileRoot: t.TempDir(), Tools: toolregistry.DefaultWithArticleFulltextClient(articlefulltext.NewClient(options))})
	for _, test := range []struct {
		id   string
		key  string
		want agentruntime.ToolResultOutcome
	}{
		{id: "PMC404", key: "pmcid", want: agentruntime.ToolResultUnavailable},
		{id: "PMC503", key: "pmcid", want: agentruntime.ToolResultUnavailable},
		{id: "PMC900", key: "pmcid", want: agentruntime.ToolResultSucceeded},
		{id: "10.1000/abstract", key: "doi", want: agentruntime.ToolResultSucceeded},
	} {
		t.Run(test.id, func(t *testing.T) {
			input := map[string]any{test.key: test.id}
			receipt := srv.executeExactToolGateway(context.Background(), "test", "", "source-"+test.id, "fetch_article_fulltext", input,
				exactServerToolGatewayOptions{PermissionSource: "direct-http", DirectExecutor: true})
			if receipt.Err != nil || receipt.BadRequest {
				t.Fatalf("real gateway: %#v", receipt)
			}
			if got := agentruntime.ClassifyToolResult(receipt.Value); got != test.want {
				t.Errorf("live typed result outcome=%s want=%s", got, test.want)
			}
			wantStatus := "completed"
			if test.want == agentruntime.ToolResultUnavailable {
				wantStatus = "unavailable"
			}
			if receipt.Status != wantStatus {
				t.Errorf("gateway audit status=%s want=%s", receipt.Status, wantStatus)
			}
			raw, err := json.Marshal(receipt.Value)
			if err != nil {
				t.Fatal(err)
			}
			var replay any
			if err := json.Unmarshal(raw, &replay); err != nil {
				t.Fatal(err)
			}
			if got := agentruntime.ClassifyToolResult(replay); got != test.want {
				t.Errorf("durable result outcome=%s want=%s", got, test.want)
			}
			wantReusable := test.want == agentruntime.ToolResultSucceeded
			if sessionRunnerReadReuseResultEligible(receipt.Value) != wantReusable {
				t.Error("read reuse confused unavailable response with successful evidence")
			}
			engine := agentruntime.Engine{Tools: agentruntime.FuncToolGateway(func(context.Context, agentruntime.ToolCall) (agentruntime.ToolResult, error) {
				return receipt.Result, nil
			})}
			batch, err := engine.ExecuteToolBatch(context.Background(), []agentruntime.ToolCall{{ID: "source-" + test.id, Name: "fetch_article_fulltext", Arguments: json.RawMessage(`{}`)}}, 0, agentruntime.MediaPolicy{}, 0)
			if err != nil || batch.NoProgress == wantReusable {
				t.Fatalf("gateway-to-engine no_progress=%t error=%v", batch.NoProgress, err)
			}
		})
	}
}
