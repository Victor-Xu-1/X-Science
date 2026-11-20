package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPRRuntimeDependencyConditionIsExactAndScoped(t *testing.T) {
	const condition = "${{ toJSON(matrix.has_go_packages) != 'false' }}"
	for _, tc := range []struct {
		name     string
		workflow string
		job      string
		mutate   func(*yaml.Node)
		want     string
	}{
		{name: "exact-current-step"},
		{name: "legacy-unconditional-step", mutate: func(step *yaml.Node) {
			for index := 0; index < len(step.Content); index += 2 {
				if step.Content[index].Value == "if" {
					step.Content = append(step.Content[:index], step.Content[index+2:]...)
					return
				}
			}
		}},
		{name: "other-workflow", workflow: ".github/workflows/quality-main.yml", want: "actions_conditional_forbidden"},
		{name: "bootstrap-job", job: "pr-quality", want: "actions_conditional_forbidden"},
		{name: "frontend-job", job: "pr-frontend-tests", want: "actions_conditional_forbidden"},
		{name: "false-condition", mutate: func(step *yaml.Node) {
			mappingValue(step, "if").Value = "${{ false }}"
		}, want: "actions_conditional_forbidden"},
		{name: "similar-unapproved-expression", mutate: func(step *yaml.Node) {
			mappingValue(step, "if").Value = "${{ matrix.has_go_packages != false }}"
		}, want: "actions_conditional_forbidden"},
		{name: "required-closure-step", mutate: func(step *yaml.Node) {
			mappingValue(step, "name").Value = "Test changed Go dependency closure"
		}, want: "actions_conditional_forbidden"},
		{name: "renamed-required-command", mutate: func(step *yaml.Node) {
			mappingValue(step, "run").Value = "go test ./..."
		}, want: "actions_conditional_forbidden"},
		{name: "custom-execution-shape", mutate: func(step *yaml.Node) {
			step.Content = append(step.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "shell"},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "bash"})
		}, want: "actions_conditional_forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := repositoryWorkflow(t, "quality-pr.yml")
			job := mappingValue(mappingValue(root, "jobs"), "pr-go-shards")
			step := relatedFrontendStep(job, "Set up runtime test dependencies")
			if step == nil || !exactString(mappingValue(step, "if"), condition) {
				t.Fatal("current runtime-dependency condition fixture is missing")
			}
			workflow, jobID := ".github/workflows/quality-pr.yml", "pr-go-shards"
			if tc.workflow != "" {
				workflow = tc.workflow
			}
			if tc.job != "" {
				jobID = tc.job
			}
			if tc.mutate != nil {
				tc.mutate(step)
			}
			state := workflowState{policy: testPolicy(), observed: map[string]int{}, currentWorkflow: workflow, currentJob: jobID}
			err := validateStep(step, &state, false, false, false)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || err.Error() != tc.want) {
				t.Fatalf("condition contract error=%v want=%q", err, tc.want)
			}
		})
	}
}
