package server

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillOriginalResourceContextRequiresCurrentRendering(t *testing.T) {
	for _, scenario := range []string{"unchanged", "resource-published"} {
		t.Run(scenario, func(t *testing.T) {
			// Capturing/materializing resources uses the existing workspace
			// owner, not execution admission or a ready computation supervisor.
			fixture := newKernelSkillDraftFixture(t)
			disableLoadedClosureFixtureMCP(t, fixture)
			const name = "original-resource-context-fixture"
			manifest := kernelSkillManifestForTest(name, "Before<$ARGUMENTS>After. Read ${X_SCIENCE_SKILL_DIR}/references/route.txt.")
			if _, err := fixture.call("host.skills.edit", name, "SKILL.md", manifest); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.call("host.skills.edit", name, "references/route.txt", "ORIGINAL_RESOURCE_BYTES"); err != nil {
				t.Fatal(err)
			}
			publishLoadedClosureFixture(t, fixture, name, false)
			run, gateway := loadedClosureFixtureGateway(fixture)
			first := executeLoadedClosureFixture(t, fixture, run, gateway, "load-original-resources", name, " resource scope ")
			originalText, full := first.Value.(string)
			if !full || !strings.Contains(originalText, "/.x-science/runtime/skills/") || strings.Contains(originalText, "${X_SCIENCE_SKILL_DIR}") {
				t.Fatal("real original resource load was not materialized in its authorized content-addressed workspace")
			}
			if scenario == "resource-published" {
				if _, err := fixture.call("host.skills.edit", name, "references/route.txt", "CHANGED_RESOURCE_BYTES", "ORIGINAL_RESOURCE_BYTES"); err != nil {
					t.Fatal(err)
				}
				publishLoadedClosureFixture(t, fixture, name, true)
			}
			prepared, keys, changed, _, _ := reopenOriginalSkillContextFixture(t, fixture, run, name)
			if scenario == "resource-published" {
				if len(keys) != 0 || len(prepared) != 0 || strings.Join(changed, ",") != name {
					t.Fatal("a newly published resource bundle replaced an original invocation without reload")
				}
				return
			}
			prompt, err := fixture.server.runtimeSkillContextFromSkills(prepared)
			if err != nil || len(keys) != 1 || keys[0] != first.SkillLoadReceipt.InvocationKey || len(changed) != 0 ||
				strings.Count(prompt, "Before< resource scope >After") != 1 || strings.Contains(prompt, "${X_SCIENCE_SKILL_DIR}") {
				t.Fatalf("exact original resource rendering was not restored: keys=%d changed=%v err=%v", len(keys), changed, err)
			}
			if len(prepared) != 1 || !strings.Contains(originalText, filepath.ToSlash(filepath.Dir(prepared[0].Path))+"/references/route.txt") {
				t.Fatal("restored resource path did not agree with the original full rendered contract")
			}
		})
	}
}
