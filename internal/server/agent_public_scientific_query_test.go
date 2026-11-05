package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"x-science/internal/tools/webfetch"
)

func TestAgentPublicScientificQueryDownloadPreservesCompleteSourceIdentity(t *testing.T) {
	for _, test := range []struct {
		name, source, filename, content, media string
		citeSource                             bool
	}{
		{"extensionless source inferred", "https://data.example.org/api/export?record=sample%2Fone&encoding=sequence&limit=1", "records.fasta", ">sample\nMST\n", "text/plain; format=fasta", false},
		{"named source cited", "https://data.example.org/records.csv?partition=one&view=full", "records.csv", "record,value\nsample,1\n", "text/csv", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAgentSaveArtifactsFixture(t)
			fetcher := &agentPublicScientificFileFetcher{body: test.content, contentType: test.media}
			fixture.server.publicScientificFiles = fetcher
			fixture.server.publicScientificDownloadSlots = make(chan struct{}, 2)
			sourceID := appendQueryScientificWebFetch(t, fixture, test.source, test.content, test.media)
			digest := sha256.Sum256([]byte(test.content))
			expected := hex.EncodeToString(digest[:])
			input := map[string]any{"url": test.source, "filename": test.filename,
				"expected_sha256": expected, "human_description": "Downloading exact source data"}
			if test.citeSource {
				input["source_tool_call_id"] = sourceID
			}
			gateway := serverAgentRuntimeToolGateway{server: fixture.server,
				taskRun: &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: fixture.stream}}}
			if preflight := gateway.agentRuntimePublicScientificSourcePreflight("download_public_scientific_file", input); preflight != nil {
				t.Fatalf("exact completed source rejected before execution: %#v", preflight)
			}
			result, err := fixture.server.executeAgentPublicScientificFileDownload(
				agentPublicScientificToolContext(t, fixture, "query-download", input), fixture.identity, "query-download", input)
			if err != nil {
				t.Fatal(err)
			}
			download := mapValue(result["download"])
			if download["source_url"] != test.source || download["source_tool_call_id"] != sourceID {
				t.Fatalf("complete source provenance changed: %#v", download)
			}
			calls := fetcher.callSnapshot()
			if len(calls) != 1 || calls[0].URL != test.source ||
				!sameStringSet(calls[0].Policy.AllowedHosts, []string{"data.example.org"}) {
				t.Fatalf("query identity or destination policy changed: %#v", calls)
			}
			stored, err := os.ReadFile(filepath.Join(fixture.projectPath, test.filename))
			if err != nil || string(stored) != test.content {
				t.Fatalf("downloaded bytes=%q err=%v", stored, err)
			}
		})
	}
}

func TestAgentPublicScientificQueryDownloadRejectsUnattestedIdentityBeforeNetwork(t *testing.T) {
	const source = "https://data.example.org/api/export?record=sample%2Fone&encoding=sequence&limit=1"
	for _, test := range []struct{ name, requested string }{
		{"changed parameter", strings.Replace(source, "limit=1", "limit=2", 1)},
		{"omitted query", "https://data.example.org/api/export"},
		{"added parameter", source + "&extra=1"},
		{"reordered query", "https://data.example.org/api/export?limit=1&encoding=sequence&record=sample%2Fone"},
		{"different path", strings.Replace(source, "/api/export?", "/api/other?", 1)},
		{"different host", strings.Replace(source, "data.example.org", "other.example.org", 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAgentSaveArtifactsFixture(t)
			fetcher := &agentPublicScientificFileFetcher{body: "unused", contentType: "text/plain"}
			fixture.server.publicScientificFiles = fetcher
			fixture.server.publicScientificDownloadSlots = make(chan struct{}, 2)
			sourceID := appendQueryScientificWebFetch(t, fixture, source, ">sample\nMST\n", "text/plain")
			input := map[string]any{"url": test.requested, "filename": "records.fasta",
				"source_tool_call_id": sourceID, "human_description": "Downloading source data"}
			gateway := serverAgentRuntimeToolGateway{server: fixture.server,
				taskRun: &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: fixture.stream}}}
			preflight := gateway.agentRuntimePublicScientificSourcePreflight("download_public_scientific_file", input)
			if preflight == nil || preflight["status"] != "scientific_download_source_preflight_required" || preflight["executed"] != false {
				t.Fatalf("unattested full URL did not receive source rejection: %#v", preflight)
			}
			_, err := fixture.server.executeAgentPublicScientificFileDownload(
				agentPublicScientificToolContext(t, fixture, "query-download", input), fixture.identity, "query-download", input)
			if !errors.Is(err, errAgentPublicScientificFileSource) || len(fetcher.callSnapshot()) != 0 {
				t.Fatalf("unattested full URL reached network: err=%v calls=%#v", err, fetcher.callSnapshot())
			}
		})
	}
}

func appendQueryScientificWebFetch(t *testing.T, fixture *agentSaveArtifactsFixture, source, content, media string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(content))
	result, err := json.Marshal(webfetch.Result{RequestedURL: source, URL: source, StatusCode: 200,
		ContentType: media, Body: content, BytesRead: len(content), ContentLength: int64(len(content)),
		BodySHA256: hex.EncodeToString(digest[:]), BodyHashScope: "returned_response_bytes", Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	return appendAgentPublicScientificSourceCheckpoint(t, fixture, "query-source", source, false, func(payload map[string]any) {
		input, marshalErr := json.Marshal(map[string]any{"url": source})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		payload["toolName"] = "web_fetch"
		payload["toolInput"] = json.RawMessage(input)
		payload["toolResult"] = json.RawMessage(result)
	})
}
