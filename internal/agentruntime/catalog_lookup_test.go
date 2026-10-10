package agentruntime

import (
	"path/filepath"
	"testing"

	"synon-go/internal/assets"
)

func TestAgentCatalogLookupIsNormalizedAndCloned(t *testing.T) {
	catalog := loadVerifiedAgentCatalog(t)
	first, found := catalog.Agent("aidd-expert")
	if !found || first.Name != "AIDD_EXPERT" {
		t.Fatalf("normalized lookup = found=%t name=%q", found, first.Name)
	}
	second, _ := catalog.Agent("AIDD_EXPERT")
	if first.Name != second.Name || len(first.SkillNames) == 0 || len(first.ConnectorIDs) == 0 {
		t.Fatalf("AIDD capability projection = %#v", first)
	}
	first.SkillNames[0] = "mutated-skill"
	first.ConnectorIDs[0] = "mutated-connector"
	again, _ := catalog.Agent("AIDD_EXPERT")
	if again.SkillNames[0] == "mutated-skill" || again.ConnectorIDs[0] == "mutated-connector" {
		t.Fatal("catalog leaked a mutable capability assignment")
	}

	agents := catalog.Agents()
	if len(agents) == 0 || !catalog.Ready() {
		t.Fatal("verified catalog is empty")
	}
	agents[0].Name = "MUTATED"
	if catalog.Agents()[0].Name == "MUTATED" {
		t.Fatal("Agents() leaked the internal catalog slice")
	}
	if _, found := catalog.Agent("missing-expert"); found {
		t.Fatal("unknown agent lookup succeeded")
	}
}

func TestLoadCatalogRejectsMissingRootOrManifest(t *testing.T) {
	if _, err := LoadCatalog(CatalogOptions{}); err == nil {
		t.Fatal("empty catalog options were accepted")
	}
	if _, err := LoadCatalog(CatalogOptions{Root: t.TempDir(), ManifestPath: filepath.Join(t.TempDir(), "missing.json")}); err == nil {
		t.Fatal("missing manifest was accepted")
	}
}

func loadVerifiedAgentCatalog(t *testing.T) *AgentCatalog {
	t.Helper()
	repositoryRoot := catalogRepositoryRoot(t)
	skillManifest, err := assets.Load(filepath.Join(repositoryRoot, "assets", "synonbiomed", "skills.manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadCatalog(CatalogOptions{
		Root:            filepath.Join(repositoryRoot, "assets", "synonbiomed", "agents"),
		ManifestPath:    filepath.Join(repositoryRoot, "assets", "synonbiomed", "agents.manifest.json"),
		AvailableSkills: skillManifest.Skills,
	})
	if err != nil {
		t.Fatalf("load verified agent catalog: %v", err)
	}
	return catalog
}
