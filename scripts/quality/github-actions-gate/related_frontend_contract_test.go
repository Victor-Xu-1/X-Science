package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRelatedFrontendWorkflowRetainsExactGraphAndFailureBoundaries(t *testing.T) {
	policy, err := loadPolicy("../../../docs/governance/github-actions-pins.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*yaml.Node)
	}{
		{"valid", func(*yaml.Node) {}},
		{"worker-missing-policy-dependency", func(jobs *yaml.Node) {
			node := mappingValue(mappingValue(jobs, "pr-frontend-related-shards"), "needs")
			node.Content = node.Content[1:]
		}},
		{"aggregate-missing-worker-dependency", func(jobs *yaml.Node) {
			node := mappingValue(mappingValue(jobs, "pr-frontend-tests"), "needs")
			node.Content = node.Content[:len(node.Content)-1]
		}},
		{"aggregate-hidden-after-cancellation", func(jobs *yaml.Node) {
			mappingValue(mappingValue(jobs, "pr-frontend-tests"), "if").Value = "success()"
		}},
		{"worker-never-executes", func(jobs *yaml.Node) {
			mappingValue(mappingValue(jobs, "pr-frontend-related-shards"), "if").Value = "false"
		}},
		{"aggregate-forges-worker-success", func(jobs *yaml.Node) {
			run := mappingValue(relatedFrontendStep(mappingValue(jobs, "pr-frontend-tests"),
				"Verify complete unique execution or original serial completion"), "run")
			run.Value = strings.Replace(run.Value, "${{ needs.pr-frontend-related-shards.result }}", "success", 1)
		}},
		{"wrong-verifier-commit", func(jobs *yaml.Node) {
			step := mappingValue(mappingValue(jobs, "pr-frontend-tests"), "steps").Content[0]
			mappingValue(mappingValue(step, "with"), "ref").Value = "main"
		}},
		{"missing-worker-parent", func(jobs *yaml.Node) {
			step := mappingValue(mappingValue(jobs, "pr-frontend-related-shards"), "steps").Content[0]
			mappingValue(mappingValue(step, "with"), "fetch-depth").Value = "1"
		}},
		{"checkout-moved-after-run", func(jobs *yaml.Node) {
			steps := mappingValue(mappingValue(jobs, "pr-frontend-tests"), "steps")
			steps.Content[0], steps.Content[1] = steps.Content[1], steps.Content[0]
		}},
		{"other-attempt-plan", func(jobs *yaml.Node) {
			step := relatedFrontendStep(mappingValue(jobs, "pr-frontend-related-shards"), "Download same-attempt preparation")
			mappingValue(mappingValue(step, "with"), "name").Value = "pr-frontend-related-plan-previous"
		}},
		{"other-run-worker-results", func(jobs *yaml.Node) {
			step := relatedFrontendStep(mappingValue(jobs, "pr-frontend-tests"), "Download every current related result")
			mappingValue(mappingValue(step, "with"), "pattern").Value = "pr-frontend-related-result-*"
		}},
		{"result-evidence-hidden-on-failure", func(jobs *yaml.Node) {
			step := relatedFrontendStep(mappingValue(jobs, "pr-frontend-related-shards"), "Upload actual worker terminal evidence")
			mappingValue(step, "if").Value = "success()"
		}},
		{"conditional-exception-does-not-escape-profile", func(jobs *yaml.Node) {
			quality := mappingValue(jobs, "pr-quality")
			quality.Content = append(quality.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "if"},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "always()"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := repositoryWorkflow(t, "quality-pr.yml")
			tc.mutate(mappingValue(root, "jobs"))
			state := workflowState{policy: policy, observed: map[string]int{}, currentWorkflow: ".github/workflows/quality-pr.yml"}
			err := validateWorkflow(state.currentWorkflow, root, &state)
			if tc.name == "valid" && err != nil {
				t.Fatal(err)
			}
			if tc.name != "valid" && err == nil {
				t.Fatal("weakened related frontend execution contract was accepted")
			}
		})
	}
}
