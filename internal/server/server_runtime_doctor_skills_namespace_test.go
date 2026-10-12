package server

import (
	"testing"

	"x-science/internal/buildinfo"
)

func TestSkillCatalogSelfContainedRecognizesCanonicalPackagedTree(t *testing.T) {
	packaged := "skills/" + buildinfo.Release().MachineSlug
	for _, path := range []string{packaged, "/owned/product/" + packaged, "builtin:x-science-runtime", "/owned/project/.x-science/skills"} {
		if !skillCatalogSelfContained([]string{path}) {
			t.Fatalf("trusted catalog path rejected: %q", path)
		}
	}
	for _, path := range []string{"", "/owned/product/" + packaged + "-copy", "/owned/product/x-science"} {
		if skillCatalogSelfContained([]string{path}) {
			t.Fatalf("unrelated catalog path accepted: %q", path)
		}
	}
}
