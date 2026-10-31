package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMainReuseReadOnlyPermissionsAndTokenAreExactlyScoped(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "quality-main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := parseWorkflow(data)
	if err != nil {
		t.Fatal(err)
	}
	if !exactRootPermissions(root, ".github/workflows/quality-main.yml") ||
		exactRootPermissions(root, ".github/workflows/quality-pr.yml") {
		t.Fatal("read-only reuse permissions are not limited to the main workflow")
	}
	for _, changed := range []string{
		strings.Replace(string(data), "actions: read", "actions: write", 1),
		strings.Replace(string(data), "checks: read", "checks: write", 1),
		strings.Replace(string(data), "pull-requests: read", "pull-requests: write", 1),
		strings.Replace(string(data), "  actions: read\n", "", 1),
		strings.Replace(string(data), "  actions: read\n", "  actions: read\n  issues: read\n", 1),
	} {
		mutated, err := parseWorkflow([]byte(changed))
		if err != nil {
			t.Fatal(err)
		}
		if exactRootPermissions(mutated, ".github/workflows/quality-main.yml") {
			t.Fatal("unexpected permission set was accepted")
		}
	}
	job := mappingValue(mappingValue(root, "jobs"), "main-go-shards")
	steps := mappingValue(job, "steps")
	var target = steps.Content[0]
	for _, step := range steps.Content {
		if name := mappingValue(step, "name"); name != nil && name.Value == "Test integrated Go dependency closure" {
			target = step
			break
		}
	}
	state := &workflowState{currentWorkflow: ".github/workflows/quality-main.yml", currentJob: "main-go-shards"}
	if !mainReuseTokenStep(target, state) {
		t.Fatal("exact read-only executor token step was rejected")
	}
	state.currentJob = "main-quality"
	if mainReuseTokenStep(target, state) {
		t.Fatal("token was accepted in another job")
	}
	state.currentJob = "main-go-shards"
	state.currentWorkflow = ".github/workflows/quality-pr.yml"
	if mainReuseTokenStep(target, state) {
		t.Fatal("token was accepted in another workflow")
	}
	state.currentWorkflow = ".github/workflows/quality-main.yml"
	run := mappingValue(target, "run")
	run.Value += "\necho unexpected"
	if mainReuseTokenStep(target, state) {
		t.Fatal("token was accepted by a modified command")
	}
}

func TestFrontendMainReuseTokenReaderIsPureUniqueAndReadOnly(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "quality-main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := func() (*yaml.Node, *yaml.Node) {
		root, err := parseWorkflow(data)
		if err != nil {
			t.Fatal(err)
		}
		job := mappingValue(mappingValue(root, "jobs"), "main-frontend-tests")
		steps := mappingValue(job, "steps")
		for _, step := range steps.Content {
			if name := mappingValue(step, "name"); name != nil && name.Value == "Read original PR frontend evidence" {
				return job, step
			}
		}
		t.Fatal("missing frontend pure reader")
		return nil, nil
	}
	state := &workflowState{currentWorkflow: ".github/workflows/quality-main.yml", currentJob: "main-frontend-tests"}
	job, step := fixture()
	if !mainReuseTokenStep(step, state) || uniqueFrontendReuseReader(mappingValue(job, "steps"), state) != nil {
		t.Fatal("exact unique frontend pure reader was rejected")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*yaml.Node)
	}{
		{"wrong-name", func(n *yaml.Node) { mappingValue(n, "name").Value = "Test integrated frontend scope" }},
		{"wrong-id", func(n *yaml.Node) { mappingValue(n, "id").Value = "other" }},
		{"capture-mode", func(n *yaml.Node) {
			mappingValue(n, "run").Value = strings.Replace(mappingValue(n, "run").Value, " fetch ", " capture ", 1)
		}},
		{"npm-added", func(n *yaml.Node) { mappingValue(n, "run").Value += "\nnpm test" }},
		{"node-added", func(n *yaml.Node) { mappingValue(n, "run").Value += "\nnode script.js" }},
		{"different-directory", func(n *yaml.Node) {
			mappingValue(n, "run").Value = strings.Replace(mappingValue(n, "run").Value, "frontend-related-reuse", "other", 1)
		}},
		{"PAT", func(n *yaml.Node) {
			mappingValue(mappingValue(n, "env"), "GITHUB_TOKEN").Value = "$" + "{{ secrets.PAT }}"
		}},
		{"extra-env", func(n *yaml.Node) {
			e := mappingValue(n, "env")
			e.Content = append(e.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "OTHER"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, changed := fixture()
			tc.mutate(changed)
			if mainReuseTokenStep(changed, state) {
				t.Fatal("modified frontend reader received a token")
			}
		})
	}
	for _, changed := range []*workflowState{
		{currentWorkflow: ".github/workflows/quality-pr.yml", currentJob: "main-frontend-tests"},
		{currentWorkflow: ".github/workflows/quality-main.yml", currentJob: "main-quality"},
		{currentWorkflow: ".github/workflows/quality-main.yml", currentJob: "main-go-shards"},
	} {
		if mainReuseTokenStep(step, changed) {
			t.Fatal("frontend token escaped its exact workflow/job")
		}
	}
	steps := mappingValue(job, "steps")
	steps.Content = append(steps.Content, step)
	if uniqueFrontendReuseReader(steps, state) == nil {
		t.Fatal("duplicate frontend token reader accepted")
	}
	job.Content = append(job.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "env"}, mappingValue(step, "env"))
	if err := validateJob(job, state, false); err == nil || err.Error() != "actions_environment_override_forbidden" {
		t.Fatalf("job-wide token was not rejected: %v", err)
	}
}
