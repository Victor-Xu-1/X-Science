package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionCounterUsesExactReadOnlyPRAndMainTransitions(t *testing.T) {
	policy, err := loadPolicy("../../../docs/governance/github-actions-pins.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ file, job string }{
		{"quality-pr.yml", "pr-quality"}, {"quality-main.yml", "main-quality"},
	} {
		relative := ".github/workflows/" + tc.file
		data, err := os.ReadFile(filepath.Join("..", "..", "..", relative))
		if err != nil {
			t.Fatal(err)
		}
		source := string(data)
		for _, mutation := range []struct{ name, old, new, want string }{
			{"valid", "", "", ""},
			{"missing", versionStepName, "Removed version check", "actions_version_transition_invalid"},
			{"constant-head", "--candidate '${{ github.sha }}'", "--candidate 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'", "actions_version_transition_invalid"},
			{"bypass", "python3 -B scripts/packaging/pr_version_gate.py", "true", "actions_version_transition_invalid"},
			{"shell-override", "      - name: " + versionStepName, "      - name: " + versionStepName + "\n        shell: true {0}", "actions_version_transition_invalid"},
			{"directory-override", "      - name: " + versionStepName, "      - name: " + versionStepName + "\n        working-directory: unrelated", "actions_version_transition_invalid"},
			{"writes", "contents: read", "contents: write", "actions_root_permissions_invalid"},
			{"token", "      - name: " + versionStepName, "      - name: " + versionStepName + "\n        env:\n          GH_TOKEN: ${{ secrets.PAT }}", "actions_environment_override_forbidden"},
		} {
			t.Run(tc.file+"/"+mutation.name, func(t *testing.T) {
				root, err := parseWorkflow([]byte(strings.Replace(source, mutation.old, mutation.new, 1)))
				if err != nil {
					t.Fatal(err)
				}
				state := workflowState{policy: policy, observed: map[string]int{}, currentWorkflow: relative}
				err = validateWorkflow(relative, root, &state)
				if mutation.want == "" && err != nil || mutation.want != "" && (err == nil || err.Error() != mutation.want) {
					t.Fatalf("error=%v want=%s", err, mutation.want)
				}
			})
		}
		root := repositoryWorkflow(t, tc.file)
		if namedRun(root, tc.job, versionStepName) != versionCommand(relative) {
			t.Fatal("version checking must remain inside the existing required policy job")
		}
	}
}

func TestVersionCounterHasNoPostMergeWriterOrCredentialException(t *testing.T) {
	for _, path := range []string{
		".github/workflows/version-pr.yml", ".github/release-please-config.json",
		".github/release-please-manifest.json", "scripts/packaging/sync_version_proposal.py",
	} {
		if _, err := os.Stat(filepath.Join("..", "..", "..", path)); !os.IsNotExist(err) {
			t.Fatalf("superseded version writer remains: %s", path)
		}
	}
}
