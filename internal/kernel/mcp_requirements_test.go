package kernel

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMCPPythonRequirementsUseValidatedPinnedContract(t *testing.T) {
	manager := &Manager{}
	requirements, err := manager.MCPPythonRequirements()
	if err != nil {
		t.Fatal(err)
	}
	if len(requirements) < 20 {
		t.Fatalf("requirements=%d want the complete MCP runtime contract", len(requirements))
	}
	seen := make(map[string]struct{}, len(requirements))
	for _, requirement := range requirements {
		key := strings.ToLower(requirement)
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("duplicate requirement %q", requirement)
		}
		seen[key] = struct{}{}
	}
	for _, required := range []string{
		"mcp==2.2.0", "mcp-types==2.2.0", "httpx==0.28.1", "httpx2==2.12.0",
		"requests==2.34.2", "jsonschema==4.26.0", "lxml==6.1.1",
	} {
		if _, found := seen[required]; !found {
			t.Fatalf("missing direct MCP dependency %q", required)
		}
	}
}

func TestMCPPythonSDKPinMatchesPackagedTestManifest(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	manifest := filepath.Join(filepath.Dir(sourceFile), "..", "..", "assets", "optional", "mcp-servers", "bio-tools", "requirements-test.txt")
	contents, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var testPin string
	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "mcp==") {
			if testPin != "" {
				t.Fatal("packaged test manifest has multiple MCP SDK pins")
			}
			testPin = line
		}
	}
	if testPin == "" {
		t.Fatal("packaged test manifest is missing an exact MCP SDK pin")
	}
	requirements, err := (&Manager{}).MCPPythonRequirements()
	if err != nil {
		t.Fatal(err)
	}
	for _, requirement := range requirements {
		if strings.EqualFold(requirement, testPin) {
			return
		}
	}
	t.Fatalf("managed runtime must use the SDK exercised by packaged protocol tests: missing %q", testPin)
}
