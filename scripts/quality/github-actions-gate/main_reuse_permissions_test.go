package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
