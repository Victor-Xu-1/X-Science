package server

import (
	"encoding/json"
	"strings"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

// X-Science receipt-consumer regression: this synthetic transport observation
// exercises the existing normalizer and receipt readers without network I/O.
func TestNativeScientificSourceFTPTransportAliasKeepsRedirectHosts(t *testing.T) {
	const source = "ftp://ftp.ncbi.nlm.nih.gov/datasets/records.csv?release=one&part=1"
	const canonical = "https://ftp.ncbi.nlm.nih.gov/datasets/records.csv?release=one&part=1"
	const final = "https://objects.example.org/data/records.csv?signature=observed"
	transport := scientificRedirectReceipt(canonical, final)
	request, err := parseAgentPublicScientificFileRequest(map[string]any{"url": source, "human_description": "Inspecting alias receipt"})
	if err != nil || request.SourceURL != source || request.DownloadURL != canonical {
		t.Fatalf("supported alias changed: request=%#v err=%v", request, err)
	}
	if observed, hosts, verified := agentPublicScientificWebFetchRedirectBinding(transport); !verified || observed != canonical ||
		!sameStringSet(hosts, []string{"objects.example.org"}) || !agentPublicScientificResultAuthorizesDownload(transport, source) {
		t.Fatalf("original receipt/alias prerequisite failed: canonical=%q hosts=%v verified=%t", observed, hosts, verified)
	}

	for _, consumer := range []string{"ordinary_completed_source", "native_acquisition_receipt"} {
		t.Run(consumer, func(t *testing.T) {
			fixture, gateway, call, ctx, requests, _ := scientificAcquisitionGatewayFixture(t, true)
			var ordinary sessionRunnerDurableToolCheckpoint
			var event transcriptstore.ProjectedEvent
			var snapshot transcriptstore.ProjectionSnapshot
			if consumer == "ordinary_completed_source" {
				ordinary = sessionRunnerDurableToolCheckpoint{ToolName: "web_fetch", ToolPhase: "completed", ToolCallID: "ftp-alias-source",
					ToolInput: mustMarshalRawMessage(map[string]any{"url": canonical}), ToolResult: mustMarshalRawMessage(transport)}
				_, appended, _, appendErr := fixture.repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
					Claim: fixture.claim, ClientMessageID: "ftp-alias-source-completed", Phase: transcriptstore.RunnerPhaseExecuting,
					PayloadJSON: mustMarshalRawMessage(ordinary), Destinations: []string{"ws"},
				})
				if appendErr != nil {
					t.Fatal(appendErr)
				}
				_, events := scientificAcquisitionEvents(t, fixture)
				for _, candidate := range events {
					if candidate.Event.EventID == appended.EventID {
						event = candidate
					}
				}
			} else {
				input := map[string]any{"url": source, "filename": "records.csv", "human_description": "Inspecting alias receipt"}
				call.Arguments = mustMarshalRawMessage(input)
				declareScientificFixtureTool(t, fixture, gateway, call, "Starting a controlled receipt parent")
				parentCtx, parent, parentInput, parsed := scientificAcquisitionParent(t, fixture, gateway, call, ctx)
				phase, phaseErr := fixture.server.newNativeScientificAcquisition(parentCtx, gateway.taskRun, parent, call.ID, parentInput, parsed)
				if phaseErr != nil {
					t.Fatal(phaseErr)
				}
				phase.ToolResult = mustMarshalRawMessage(normalizeNativeScientificSourceResult(mustMarshalRawMessage(map[string]any{"ok": true, "result": transport})))
				phase.ResultSHA256 = scientificSourceDigest(phase.ToolResult)
				if phaseErr := fixture.server.checkpointNativeScientificAcquisition(parentCtx, parent, phase, "source_acquisition_completed"); phaseErr != nil {
					t.Fatal(phaseErr)
				}
				var events []transcriptstore.ProjectedEvent
				snapshot, events = scientificAcquisitionEvents(t, fixture)
				for _, candidate := range events {
					var saved nativeScientificAcquisition
					if json.Unmarshal(candidate.Event.PayloadJSON, &saved) == nil && saved.ToolCallID == phase.ToolCallID && saved.ToolPhase == "source_acquisition_completed" {
						event = candidate
					}
				}
			}
			if event.Event.EventID == 0 {
				t.Fatal("missing actual committed synthetic receipt")
			}
			for _, tc := range []struct {
				name, expected string
				authorized     bool
			}{
				{"supported_alias", source, true},
				{"different_query", strings.Replace(source, "release=one", "release=two", 1), false},
				{"foreign_ftp_host", strings.Replace(source, "ftp.ncbi.nlm.nih.gov", "foreign.example.org", 1), false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					parsed, parseErr := parseAgentPublicScientificFileRequest(map[string]any{"url": tc.expected, "human_description": "Checking exact alias boundary"})
					if tc.name == "foreign_ftp_host" {
						if parseErr == nil {
							t.Fatal("normalizer accepted arbitrary FTP host")
						}
						parsed.SourceURL = tc.expected // Also test the receipt reader's invalid-input boundary.
					} else if parseErr != nil {
						t.Fatal(parseErr)
					}
					if agentPublicScientificURLsEquivalent(canonical, tc.expected) != tc.authorized ||
						agentPublicScientificResultAuthorizesDownload(transport, tc.expected) != tc.authorized {
						t.Fatal("existing exact query/FTP alias authority changed")
					}
					var binding agentPublicScientificSourceBinding
					var matched bool
					var readErr error
					if consumer == "ordinary_completed_source" {
						binding, matched = fixture.server.agentPublicScientificCompletedCheckpointBinding(ordinary, transport, event, parsed)
					} else {
						binding, _, matched, readErr = fixture.server.nativeScientificAcquisitionSource(ctx, snapshot, event, tc.expected)
					}
					if readErr != nil || matched != tc.authorized {
						t.Fatalf("receipt authority matched=%t want=%t err=%v", matched, tc.authorized, readErr)
					}
					if tc.authorized && !sameStringSet(binding.RedirectHosts, []string{"objects.example.org"}) {
						t.Errorf("authorized FTP alias lost actual HTTPS redirect hosts: %#v", binding)
					}
					if !tc.authorized && len(binding.RedirectHosts) != 0 {
						t.Fatal("different query or foreign FTP expanded redirect authority")
					}
				})
			}
			if requests.Load() != 0 {
				t.Fatalf("receipt-only test made %d HTTP requests", requests.Load())
			}
		})
	}
}
