package bashsource

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkerStartupIdentityIsClosedAndLegacyCompatible(t *testing.T) {
	generation := strings.Repeat("a", 64)
	for _, c := range []struct {
		policy                     WorkerStartupPolicy
		generation, kind, language string
		valid                      bool
	}{
		{"", "", "analysis", "python", true},
		{CanonicalBashStdlibV1, generation, "bash", "python", true},
		{"unknown", generation, "bash", "python", false},
		{CanonicalBashStdlibV1, "", "bash", "python", false},
		{"", generation, "bash", "python", false},
		{CanonicalBashStdlibV1, strings.Repeat("A", 64), "bash", "python", false},
		{CanonicalBashStdlibV1, generation, "analysis", "python", false},
		{CanonicalBashStdlibV1, generation, "bash", "r", false},
	} {
		if err := ValidateWorkerStartup(c.policy, c.generation, c.kind, c.language); (err == nil) != c.valid {
			t.Fatalf("startup %#v error=%v", c, err)
		}
	}
}

func TestRestrictedWorkerAcceptsOnlyCanonicalExecutionFrames(t *testing.T) {
	code, err := BashPythonWrapper("printf hello")
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{`{"type":"host_ack"}`, `{"type":"host_result_start","result":{}}`} {
		if err := ValidateWorkerFrame(CanonicalBashStdlibV1, []byte(payload)); err == nil {
			t.Fatal("restricted supervisor admitted a host RPC frame")
		}
	}
	for _, c := range []struct {
		code  string
		host  bool
		valid bool
	}{
		{code, false, true}, {code, true, false}, {"print('arbitrary')", false, false},
		{code + "\nprint('arbitrary')", false, false},
	} {
		payload, _ := json.Marshal(map[string]any{"code": c.code, "host_enabled": c.host})
		if err := ValidateWorkerFrame(CanonicalBashStdlibV1, payload); (err == nil) != c.valid {
			t.Fatalf("frame host=%v valid=%v error=%v", c.host, c.valid, err)
		}
		if err := ValidateWorkerFrame("", payload); err != nil {
			t.Fatal("legacy changed", err)
		}
	}
}
