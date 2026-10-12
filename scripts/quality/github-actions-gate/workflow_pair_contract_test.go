package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func controlledArtifactBrowserJobContract(root *yaml.Node, jobID, testStep, expectedRun string, policy actionsPolicy) error {
	jobs := mappingValue(root, "jobs")
	job := mappingValue(jobs, jobID)
	if jobs == nil || len(jobs.Content) != 10 || job == nil || !exactRootPermissions(root) ||
		mappingValue(job, "env") != nil || mappingValue(job, "permissions") != nil ||
		mappingValue(job, "container") != nil || mappingValue(job, "services") != nil ||
		mappingValue(job, "if") != nil {
		return fmt.Errorf("controlled browser must stay in the existing read-only frontend job")
	}
	steps := mappingValue(job, "steps")
	if steps == nil || steps.Kind != yaml.SequenceNode {
		return fmt.Errorf("frontend steps missing")
	}
	var setup, test, evidence *yaml.Node
	for _, step := range steps.Content {
		name := mappingValue(step, "name")
		if name == nil {
			continue
		}
		switch name.Value {
		case "Set up controlled browser test Go runtime":
			if setup != nil {
				return fmt.Errorf("duplicate controlled browser Go setup")
			}
			setup = step
		case testStep:
			test = step
		case "Upload controlled browser test evidence":
			evidence = step
		}
	}
	goPin := policy.Actions["actions/setup-go"]
	uploadPin := policy.Actions["actions/upload-artifact"]
	if setup == nil || !exactString(mappingValue(setup, "uses"), "actions/setup-go@"+goPin.SHA) ||
		mappingValue(setup, "if") != nil || mappingValue(setup, "env") != nil ||
		!exactString(mappingValue(mappingValue(setup, "with"), "go-version-file"), "go.mod") ||
		!exactString(mappingValue(mappingValue(setup, "with"), "cache-dependency-path"), "go.sum") {
		return fmt.Errorf("controlled browser requires the approved Go runtime")
	}
	if test == nil || mappingValue(test, "if") != nil || mappingValue(test, "env") != nil ||
		strings.TrimSpace(namedRun(root, jobID, testStep)) != expectedRun {
		return fmt.Errorf("controlled browser scope cannot be skipped or replaced")
	}
	retention := mappingValue(mappingValue(evidence, "with"), "retention-days")
	if evidence == nil || !exactString(mappingValue(evidence, "uses"), "actions/upload-artifact@"+uploadPin.SHA) ||
		!unconditionalTestEvidenceStep(evidence) || retention == nil || retention.Tag != "!!int" || retention.Value != "7" {
		return fmt.Errorf("controlled browser evidence must retain the approved bounded upload")
	}
	return nil
}

func TestControlledArtifactBrowserUsesExistingSymmetricFrontendJobs(t *testing.T) {
	policy, err := loadPolicy(filepath.Join("..", "..", "..", "docs", "governance", "github-actions-pins.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range []struct {
		workflow, job, step, base string
	}{
		{"quality-pr.yml", "pr-frontend-tests", "Test changed frontend scope", "$" + "{{ github.event.pull_request.base.sha }}"},
		{"quality-main.yml", "main-frontend-tests", "Test integrated frontend scope", "$" + "{{ github.event.before }}"},
	} {
		t.Run(contract.workflow, func(t *testing.T) {
			expectedRun := "PYTHONDONTWRITEBYTECODE=1 python3 scripts/quality/pr_fast_scope.py --frontend --base '" +
				contract.base + "' --head '$" + "{{ github.sha }}' --log-dir \"$RUNNER_TEMP/runtime-test-evidence\""
			root := repositoryWorkflow(t, contract.workflow)
			if err := controlledArtifactBrowserJobContract(root, contract.job, contract.step, expectedRun, policy); err != nil {
				t.Fatal(err)
			}
			for _, mutation := range []string{"skip-test", "replace-scope", "skip-evidence", "unapproved-go", "global-env", "extra-job"} {
				t.Run(mutation, func(t *testing.T) {
					changed := repositoryWorkflow(t, contract.workflow)
					jobs := mappingValue(changed, "jobs")
					job := mappingValue(jobs, contract.job)
					steps := mappingValue(job, "steps")
					for _, step := range steps.Content {
						name := mappingValue(step, "name")
						if name == nil {
							continue
						}
						if mutation == "skip-test" && name.Value == contract.step {
							step.Content = append(step.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "if"},
								&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "false"})
						}
						if mutation == "replace-scope" && name.Value == contract.step {
							mappingValue(step, "run").Value = "echo skipped"
						}
						if mutation == "skip-evidence" && name.Value == "Upload controlled browser test evidence" {
							mappingValue(step, "if").Value = "success()"
						}
						if mutation == "unapproved-go" && name.Value == "Set up controlled browser test Go runtime" {
							mappingValue(step, "uses").Value = "actions/setup-go@main"
						}
					}
					if mutation == "global-env" {
						job.Content = append(job.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "env"},
							&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
					}
					if mutation == "extra-job" {
						jobs.Content = append(jobs.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "browser-matrix"},
							&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
					}
					if err := controlledArtifactBrowserJobContract(changed, contract.job, contract.step, expectedRun, policy); err == nil {
						t.Fatal("weakened hosted browser workflow was accepted")
					}
				})
			}
		})
	}
}

func TestWorkflowBootstrapMappingRejectsConflictsAndInvalidIDs(t *testing.T) {
	for _, name := range []string{"main conflict", "invalid PR ID", "invalid workflow path"} {
		t.Run(name, func(t *testing.T) {
			policy := testPolicy()
			policy.AllowedWorkflows = []string{".github/workflows/quality-pr.yml", ".github/workflows/quality.yml"}
			policy.WorkflowBootstrapJobs = map[string]string{
				".github/workflows/quality-pr.yml": "pr", ".github/workflows/quality.yml": "test",
			}
			policy.WorkflowCheckouts = map[string]checkoutContract{
				".github/workflows/quality-pr.yml": {Ref: "$" + "{{ github.sha }}", FetchDepth: 2},
				".github/workflows/quality.yml":    {Ref: "$" + "{{ github.sha }}", FetchDepth: 1},
			}
			switch name {
			case "main conflict":
				policy.WorkflowBootstrapJobs[policy.BootstrapWorkflow] = "different"
			case "invalid PR ID":
				policy.WorkflowBootstrapJobs[".github/workflows/quality-pr.yml"] = "pr quality"
			case "invalid workflow path":
				policy.AllowedWorkflows[0] = ".github/workflows/sub/quality.yml"
			}
			if err := validatePolicy(policy); err == nil {
				t.Fatal("invalid workflow authority was accepted")
			}
		})
	}
}

func repositoryWorkflow(t *testing.T, name string) *yaml.Node {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	root, err := parseWorkflow(data)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func namedRun(root *yaml.Node, job, step string) string {
	steps := mappingValue(mappingValue(mappingValue(root, "jobs"), job), "steps")
	if steps == nil {
		return ""
	}
	for _, item := range steps.Content {
		if name := mappingValue(item, "name"); name != nil && name.Value == step {
			if run := mappingValue(item, "run"); run != nil {
				return strings.TrimSpace(run.Value)
			}
		}
	}
	return ""
}

func TestRepositoryWorkflowRoutingAndChangedScopeRuntimeEnvironment(t *testing.T) {
	full := repositoryWorkflow(t, "quality.yml")
	pr := repositoryWorkflow(t, "quality-pr.yml")
	main := repositoryWorkflow(t, "quality-main.yml")
	fullEvents := mappingValue(full, "on")
	if fullEvents == nil || len(fullEvents.Content) != 4 || mappingValue(fullEvents, "workflow_dispatch") == nil {
		t.Fatal("full CI must be scheduled and manually dispatched, without redundant push/PR runs")
	}
	schedule := mappingValue(fullEvents, "schedule")
	if schedule == nil || len(schedule.Content) != 1 {
		t.Fatal("expected one daily full-quality schedule")
	}
	cron, timezone := mappingValue(schedule.Content[0], "cron"), mappingValue(schedule.Content[0], "timezone")
	if cron == nil || cron.Value != "0 2 * * *" || timezone == nil || timezone.Value != "Asia/Shanghai" {
		t.Fatal("full CI must run daily at 02:00 Asia/Shanghai")
	}
	prEvents := mappingValue(pr, "on")
	if prEvents == nil || len(prEvents.Content) != 2 || mappingValue(prEvents, "pull_request") == nil {
		t.Fatal("fast CI must run for pull requests")
	}
	mainEvents := mappingValue(main, "on")
	push := mappingValue(mainEvents, "push")
	branches := mappingValue(push, "branches")
	if mainEvents == nil || push == nil || branches == nil || len(branches.Content) != 1 ||
		branches.Content[0].Value != "main" {
		t.Fatal("main integration CI must run only for main pushes")
	}
	fullSetup := namedRun(full, "runtime-quality", "Set up runtime test dependencies")
	if fullSetup == "" || namedRun(pr, "pr-go-shards", "Set up runtime test dependencies") != fullSetup {
		t.Fatal("PR runtime tests must retain the full runtime environment")
	}
	if namedRun(main, "main-go-shards", "Set up runtime test dependencies") != fullSetup {
		t.Fatal("main affected runtime tests must retain the full runtime environment")
	}
	for _, workflow := range []struct {
		root *yaml.Node
		jobs []string
	}{
		{pr, []string{"pr-quality", "pr-go-shards", "pr-frontend-tests"}},
		{main, []string{"main-quality", "main-go-shards", "main-frontend-tests"}},
	} {
		for _, job := range workflow.jobs {
			steps := mappingValue(mappingValue(mappingValue(workflow.root, "jobs"), job), "steps")
			checkout := steps.Content[0]
			inputs := mappingValue(checkout, "with")
			depth := mappingValue(inputs, "fetch-depth")
			ref := mappingValue(inputs, "ref")
			if depth == nil || depth.Value != "2" || ref == nil || ref.Value != "$"+"{{ github.sha }}" {
				t.Fatalf("%s checkout must bind the exact change commit and its parent", job)
			}
			for _, step := range steps.Content {
				if run := mappingValue(step, "run"); run != nil && strings.Contains(run.Value, "git fetch") {
					t.Fatal("private change checkout must not fetch after credentials were removed")
				}
			}
		}
	}
}
