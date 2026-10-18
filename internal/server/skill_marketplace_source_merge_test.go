package server

import (
	"strings"
	"testing"
)

func TestMarketplaceSourceMergePreservesLegacyProvenanceAndSourceIdentity(t *testing.T) {
	license := "Apache-2.0"
	legacy := webSkillMarketplaceSource{Slug: "acme-skills", Repo: "https://github.com/acme/skills",
		SHA: marketplaceTestSHA, License: &license, Skills: []string{"first"},
		ImportedAt: "older-import", Removable: false, Hashes: map[string]string{"first": "old-bytes"}}
	incoming := webSkillMarketplaceSource{Slug: legacy.Slug, Repo: legacy.Repo,
		SHA: marketplaceTestSHA, Skills: []string{"second"}, ImportedAt: "newer-import", Removable: true,
		Hashes: map[string]string{"second": "new-bytes"},
		SkillRevisions: map[string]webSkillMarketplaceSkillRevision{"second": {
			SHA: marketplaceTestSHA, License: &license, LicenseRef: marketplaceTestSHA, ImportedAt: "newer-import",
		}}}
	t.Run("legacy-facts-are-not-replaced-or-upgraded", func(t *testing.T) {
		merged, err := mergeSkillMarketplaceSource(legacy, incoming)
		if err != nil {
			t.Fatal(err)
		}
		first := merged.SkillRevisions["first"]
		if len(merged.Skills) != 2 || merged.SHA != marketplaceTestSHA || merged.Removable ||
			merged.Hashes["first"] != "old-bytes" || first.SHA != marketplaceTestSHA || first.ImportedAt != "older-import" {
			t.Fatalf("legacy member facts were overwritten: %#v", merged)
		}
		if first.License == nil || *first.License != license || first.LicenseRef != "" || merged.License != nil {
			t.Fatalf("old HEAD license was invented as pinned evidence: first=%#v, aggregate=%v", first, merged.License)
		}
		merged.Hashes["first"] = "changed-copy"
		if legacy.Hashes["first"] != "old-bytes" || legacy.SkillRevisions != nil || incoming.Hashes["first"] != "" {
			t.Fatal("merge mutated a caller-owned source record")
		}
	})
	t.Run("unknown-pin-stays-unknown", func(t *testing.T) {
		unknown := legacy
		unknown.SHA = ""
		merged, err := mergeSkillMarketplaceSource(unknown, incoming)
		if err != nil || merged.SHA != "" || merged.SkillRevisions["first"].SHA != "" || merged.License != nil {
			t.Fatalf("unknown old pin was relabeled as the new commit: %#v, %v", merged, err)
		}
	})
	t.Run("repository-slug-collision-is-not-a-merge", func(t *testing.T) {
		other := incoming
		other.Repo = "https://github.com/acme-skills/other"
		if _, err := mergeSkillMarketplaceSource(legacy, other); err == nil {
			t.Fatal("different repositories with the same slug were silently combined")
		}
	})
	t.Run("mixed-pins-do-not-propagate-one-pin", func(t *testing.T) {
		other := incoming
		other.SkillRevisions = map[string]webSkillMarketplaceSkillRevision{"second": {SHA: strings.Repeat("a", 40)}}
		merged, err := mergeSkillMarketplaceSource(legacy, other)
		if err != nil || merged.SHA != "" || merged.License != nil || merged.SkillRevisions["first"].SHA != marketplaceTestSHA {
			t.Fatalf("mixed provenance was replaced by a global pin: %#v, %v", merged, err)
		}
	})
}
