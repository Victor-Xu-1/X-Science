package assets

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

var historicalHumanBrand = regexp.MustCompile(`(?i)\bsynon(?:[ -]?biomed|ai)?(?:[ \t\r\n]+|[.,!?;:()\[\]{}](?:[ \t\r\n]+|$)|$)`)
var historicalBridgeHumanBrand = regexp.MustCompile(`(?i)\bsynon-go[ \t\r\n]+(?:live|LSP|MCP|SDK)(?:[ \t\r\n]+|$)`)
var runtimeMachineToken = regexp.MustCompile(`^[a-z0-9_.:/-]+$`)

func TestCurrentRuntimeHumanLabelsUseXScience(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate repository root")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	for _, directory := range []string{"cmd", "internal", "scripts/smoke-live-im"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			// These capture historical source/protocol identities, not current UI.
			if strings.HasPrefix(filepath.ToSlash(relative), "internal/compat/") {
				return nil
			}
			locations := token.NewFileSet()
			parsed, err := parser.ParseFile(locations, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(literal.Value)
				if err == nil && containsHistoricalHumanBrand(value) && !historicalLabelReadCompatibility(relative, value) {
					t.Errorf("current human label retains historical brand at %s", locations.Position(literal.Pos()))
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, relative := range []string{
		"internal/server/structure_interaction_diagram.py", "internal/server/structure_interaction_renderer.py",
		"internal/agentruntime/assets/x_science_platform_policy.md",
		"skills/x-science/ngs-analysis-router/SKILL.md",
		"skills/x-science/nextflow-development/references/geo-sra-acquisition.md",
		"internal/logoassets/SOURCE.md", "internal/server/assets/THIRD_PARTY_LICENSES.md",
		"internal/sciencecapability/executionpacks/autodock_vina.py",
		"internal/sciencecapability/executionpacks/autodock_vina_outputs.py",
		"skills/x-science/autodock-vina/scripts/autodock_vina.py",
		"skills/x-science/autodock-vina/scripts/autodock_vina_outputs.py",
		"internal/server/structure_minimization.py",
		"skills/x-science/skill-creator/scripts/quick_validate.py",
	} {
		raw, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		if containsHistoricalHumanBrand(string(raw)) {
			t.Errorf("current runtime text retains historical brand: %s", relative)
		}
	}
}

func containsHistoricalHumanBrand(value string) bool {
	// A bare lowercase machine ID remains a compatibility contract. Human
	// prose, capitalized names and sentences are distinct from that ID.
	return !runtimeMachineToken.MatchString(value) &&
		(historicalHumanBrand.MatchString(value) || historicalBridgeHumanBrand.MatchString(value))
}

func historicalLabelReadCompatibility(path, value string) bool {
	switch filepath.ToSlash(path) {
	case "internal/server/runner_brand_compatibility.go":
		return value == "Synon durable correction state from a prior bounded execution unit" ||
			value == "\nSynon no-progress recovery state: "
	case "internal/kernel/confinement.go":
		return value == "Synon kernel confinement is unavailable on this platform"
	case "internal/server/structure_interaction_diagram_api.go":
		return value == "Synon 2D Interaction Engine"
	default:
		return false
	}
}

func TestRuntimeHumanLabelBoundaryKeepsMachineCompatibility(t *testing.T) {
	for _, value := range []string{"Synon", "SynonAI Image Generation", "Synon Link", "Synon-Biomed report", "Synon Biomed report", "SYNON", "synon access", "Synon kernel", "You are Synon.", "You are Synon, a concise assistant.", "Synon: warning", "method not supported by synon-go MCP client", "synon-go live bridge"} {
		if !containsHistoricalHumanBrand(value) {
			t.Errorf("historical human label was missed: %q", value)
		}
	}
	for _, value := range []string{"xscience", "x-science/internal/config", "x-science.kernel.v1", "X-Science-CSRF-Token", "xscience-v1.1.sqlite", "X_SCIENCE_CONFIG", "Synonym", "synonyms", "X-Science", "x-science serve --config x-science.json", "Usage: x-science mcp-ketcher --stdio"} {
		if containsHistoricalHumanBrand(value) {
			t.Errorf("machine/biological identifier was treated as human branding: %q", value)
		}
	}
	if historicalLabelReadCompatibility("internal/server/prompt.go", "Synon no-progress recovery state: ") ||
		historicalLabelReadCompatibility("internal/kernel/confinement.go", "Synon newly emitted label") ||
		historicalLabelReadCompatibility("internal/server/prompt.go", "synonBiomed") ||
		historicalLabelReadCompatibility("internal/server/transcript_web_history.go", "Synon report") {
		t.Fatal("historical reader exception escaped its exact file/value boundary")
	}
}

func TestHistoricalHumanLabelExceptionIsExactReaderInputOnly(t *testing.T) {
	for path, value := range map[string]string{
		"internal/server/runner_brand_compatibility.go":        "Synon durable correction state from a prior bounded execution unit",
		"internal/kernel/confinement.go":                       "Synon kernel confinement is unavailable on this platform",
		"internal/server/structure_interaction_diagram_api.go": "Synon 2D Interaction Engine",
	} {
		if !historicalLabelReadCompatibility(path, value) || historicalLabelReadCompatibility(path, value+" new output") || historicalLabelReadCompatibility("internal/server/prompt.go", value) {
			t.Fatal("historical reader admission is not exact")
		}
	}
}

func TestRuntimeAndModelSurfacesUseOnlyXScienceProductIdentity(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate repository root")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	for _, relative := range []string{
		"internal/agentruntime", "internal/kernel", "internal/server", "internal/software", "internal/tools",
	} {
		walkGoStringLiterals(t, filepath.Join(root, relative))
	}
	for _, relative := range []string{
		"assets/x-science/agents", "skills/x-science",
	} {
		walkModelTextFiles(t, filepath.Join(root, relative))
	}
}

func walkGoStringLiterals(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err == nil && containsReferenceProductLabel(value) && !isPersistedRuntimeAlias(path, value) {
				t.Errorf("runtime string leaks reference product identity: %s", path)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Historical configuration IDs are not display labels or model instructions.
// Permit only these exact inputs at their single normalization boundary; all
// other strings, including new strings in that file, remain subject to the gate.
func isPersistedRuntimeAlias(path, value string) bool {
	if !strings.HasSuffix(filepath.ToSlash(path), "/internal/kernel/conda_runtime_identity.go") {
		return false
	}
	return value == "claude-science-python" || value == "claude-science-r"
}

func TestPersistedRuntimeAliasExceptionIsPathAndValueScoped(t *testing.T) {
	path := "/repo/internal/kernel/conda_runtime_identity.go"
	if !isPersistedRuntimeAlias(path, "claude-science-python") ||
		isPersistedRuntimeAlias("/repo/internal/kernel/prompt.go", "claude-science-python") ||
		isPersistedRuntimeAlias(path, "claude-science-new-label") {
		t.Fatal("legacy identity exception escaped its exact path/value boundary")
	}
}

func walkModelTextFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || strings.Contains(strings.ToUpper(entry.Name()), "THIRD_PARTY") ||
			strings.HasSuffix(entry.Name(), ".orig") {
			return nil
		}
		extension := strings.ToLower(filepath.Ext(path))
		if extension != ".md" && extension != ".yaml" && extension != ".yml" && extension != ".json" && extension != ".py" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if containsReferenceProductLabel(string(raw)) {
			t.Errorf("model-visible text leaks reference product identity: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func containsReferenceProductLabel(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "claude science") || strings.Contains(value, "claude-science") ||
		strings.Contains(value, "llm science")
}
