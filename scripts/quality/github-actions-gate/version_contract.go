package main

import (
	"errors"
	"strings"

	"gopkg.in/yaml.v3"
)

const versionStepName = "Verify one PR version increment"

func versionCommand(relative string) string {
	prefix := "PYTHONDONTWRITEBYTECODE=1 python3 -B scripts/packaging/pr_version_gate.py \\\n"
	switch relative {
	case ".github/workflows/quality-pr.yml":
		return prefix + "  --base '${{ github.event.pull_request.base.sha }}' --candidate '${{ github.sha }}'"
	case ".github/workflows/quality-main.yml":
		return prefix + "  --base '${{ github.event.before }}' --candidate '${{ github.sha }}' \\\n" +
			"  --initial-main '${{ github.event.created }}'"
	default:
		return ""
	}
}

// The counter is read-only and shares the existing required policy job.
// It cannot be replaced by a constant revision, post-merge writer or PR bypass.
func validateVersionTransitionStep(relative string, jobs *yaml.Node, bootstrap string) error {
	want := versionCommand(relative)
	if want == "" {
		return nil
	}
	steps := mappingValue(mappingValue(jobs, bootstrap), "steps")
	count := 0
	if steps != nil {
		for _, step := range steps.Content {
			name := mappingValue(step, "name")
			if name == nil || name.Value != versionStepName {
				continue
			}
			count++
			run := mappingValue(step, "run")
			if run == nil || strings.TrimSpace(run.Value) != want {
				return errors.New("actions_version_transition_invalid")
			}
		}
	}
	if count != 1 {
		return errors.New("actions_version_transition_invalid")
	}
	return nil
}
