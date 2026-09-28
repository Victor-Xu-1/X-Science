package server

import (
	"context"
	"encoding/json"
	"testing"

	"synon-go/internal/agentruntime"
	transcriptstore "synon-go/internal/persistence/transcript"
)

// Admission has neither the worker namespace nor evidence of a worker restart.
// A checkpoint claim must not turn valid persistent Python into a prestart failure.
func TestGatewayPythonNamespaceBelongsToWorker(t *testing.T) {
	for _, source := range []transcriptstore.ResumeSource{transcriptstore.ResumeSourceFresh, transcriptstore.ResumeSourceCheckpoint} {
		gateway := serverAgentRuntimeToolGateway{
			server: &Server{}, allowedTools: []string{"repl"},
			taskRun: &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{
				Claim: transcriptstore.RunnerClaim{ResumeSource: source, Attempt: 2},
			}},
		}
		for name, code := range map[string]string{
			"comma imports":      "import json, os\nprint(os.path.basename('example'))\nprint(json.dumps({}))",
			"alias":              "import json as encoder\nprint(encoder.dumps({}))",
			"from aliases":       "from collections import Counter as Counts, defaultdict as Mapping\nprint(Counts.__name__, Mapping.__name__)",
			"persistent module":  "print(json.dumps({'ready': True}))",
			"persistent result":  "print(prior_result.get('records'))",
			"function parameter": "def read(json):\n return json.get('value')\nprint(read({'value': 7}))",
			"dynamic binding":    "globals()['record'] = {'value': 7}\nprint(record.get('value'))",
		} {
			t.Run(string(source)+"/"+name, func(t *testing.T) {
				arguments, err := json.Marshal(map[string]any{"code": code, "human_description": "Inspecting results"})
				if err != nil {
					t.Fatal(err)
				}
				call := agentruntime.ToolCall{ID: name, Name: "repl", Arguments: arguments}
				if diagnostic := gateway.toolCallPreflightDiagnostic(context.Background(), call); diagnostic != "" {
					t.Fatalf("admission guessed worker namespace: %s", diagnostic)
				}
			})
		}
	}
}
