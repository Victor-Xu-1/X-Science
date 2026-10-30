package main

import (
	"errors"
	"strings"

	"gopkg.in/yaml.v3"
)

const relatedFrontendWorkflow = ".github/workflows/quality-pr.yml"
const relatedFrontendWorkerCondition = "needs.pr-frontend-plan.result == 'success' && needs.pr-frontend-plan.outputs.mode == 'sharded'"
const relatedFrontendResultsCondition = "needs.pr-frontend-plan.outputs.mode == 'sharded'"
const relatedFrontendPlanArtifact = "pr-frontend-related-plan-${{ github.run_id }}-${{ github.run_attempt }}"
const relatedFrontendResultArtifact = "pr-frontend-related-result-${{ github.run_id }}-${{ github.run_attempt }}"
const relatedFrontendPrepareRun = `PYTHONDONTWRITEBYTECODE=1 python3 -B scripts/quality/frontend_related_ci.py prepare --directory "$RUNNER_TEMP/frontend-related-plan"`
const relatedFrontendWorkerRun = `PYTHONDONTWRITEBYTECODE=1 python3 -B scripts/quality/frontend_related_ci.py worker \
  --plan "$RUNNER_TEMP/frontend-related-plan/plan.json" \
  --plan-digest '${{ needs.pr-frontend-plan.outputs.plan-sha256 }}' \
  --directory "$RUNNER_TEMP/frontend-related-worker-${{ matrix.index }}" \
  --index '${{ matrix.index }}'`
const relatedFrontendAggregateRun = `PYTHONDONTWRITEBYTECODE=1 python3 -B scripts/quality/frontend_related_ci.py aggregate \
  --plan "$RUNNER_TEMP/frontend-related-plan/plan.json" \
  --plan-digest '${{ needs.pr-frontend-plan.outputs.plan-sha256 }}' \
  --directory "$RUNNER_TEMP/frontend-related-results" \
  --preparation-result '${{ needs.pr-frontend-plan.result }}' \
  --shard-result '${{ needs.pr-frontend-related-shards.result }}'`

// Only this complete, checked graph may use the two intentional job conditions.
// Every job still passes the original dependency, checkout, pin and safety checks.
func validateRelatedFrontendWorkflow(relative string, jobs *yaml.Node) (bool, error) {
	if relative != relatedFrontendWorkflow {
		return false, nil
	}
	plan := mappingValue(jobs, "pr-frontend-plan")
	worker := mappingValue(jobs, "pr-frontend-related-shards")
	aggregate := mappingValue(jobs, "pr-frontend-tests")
	if plan == nil && worker == nil && mappingValue(aggregate, "if") == nil {
		return false, nil
	}
	invalid := errors.New("actions_frontend_related_contract_invalid")
	if !exactString(mappingValue(plan, "needs"), "pr-quality") || mappingValue(plan, "if") != nil ||
		!exactStringSequence(mappingValue(worker, "needs"), []string{"pr-quality", "pr-frontend-plan"}) ||
		!exactString(mappingValue(worker, "if"), relatedFrontendWorkerCondition) ||
		!exactStringSequence(mappingValue(aggregate, "needs"), []string{"pr-quality", "pr-frontend-plan", "pr-frontend-related-shards"}) ||
		!exactString(mappingValue(aggregate, "if"), "always()") ||
		!exactString(mappingValue(aggregate, "name"), "PR frontend scope") {
		return false, invalid
	}
	for _, job := range []*yaml.Node{plan, worker, aggregate} {
		steps := mappingValue(job, "steps")
		if steps == nil || steps.Kind != yaml.SequenceNode || len(steps.Content) == 0 {
			return false, invalid
		}
		checkout := steps.Content[0]
		uses := mappingValue(checkout, "uses")
		if uses == nil || !strings.HasPrefix(uses.Value, "actions/checkout@") ||
			!exactCheckoutInputs(checkout, checkoutContract{Ref: "${{ github.sha }}", FetchDepth: 2}) {
			return false, errors.New("actions_checkout_contract_invalid")
		}
	}
	outputs := mappingValue(plan, "outputs")
	if !mappingHasExactKeys(outputs, "mode", "matrix", "node-version", "plan-sha256") {
		return false, invalid
	}
	for _, name := range []string{"mode", "matrix", "node-version", "plan-sha256"} {
		if !exactString(mappingValue(outputs, name), "${{ steps.frontend.outputs."+name+" }}") {
			return false, invalid
		}
	}
	prepare := relatedFrontendStep(plan, "Preserve frontend checks and plan exact related workers")
	strategy := mappingValue(worker, "strategy")
	if !exactString(mappingValue(prepare, "id"), "frontend") ||
		!relatedFrontendRun(prepare, relatedFrontendPrepareRun) ||
		!exactString(mappingValue(strategy, "matrix"), "${{ fromJSON(needs.pr-frontend-plan.outputs.matrix) }}") ||
		!relatedFrontendScalar(mappingValue(strategy, "fail-fast"), "!!bool", "false") ||
		!relatedFrontendScalar(mappingValue(strategy, "max-parallel"), "!!int", "4") ||
		!relatedFrontendRun(relatedFrontendStep(worker, "Execute independently bound exact partition"), relatedFrontendWorkerRun) ||
		!relatedFrontendRun(relatedFrontendStep(aggregate, "Require original preparation success"),
			"test '${{ needs.pr-frontend-plan.result }}' = 'success'") ||
		!relatedFrontendRun(relatedFrontendStep(aggregate, "Verify complete unique execution or original serial completion"), relatedFrontendAggregateRun) {
		return false, invalid
	}
	if !relatedFrontendArtifact(relatedFrontendStep(plan, "Upload same-attempt related plan"), "actions/upload-artifact",
		map[string]string{"name": relatedFrontendPlanArtifact, "path": "${{ runner.temp }}/frontend-related-plan/", "if-no-files-found": "error", "retention-days": "7"}) ||
		!relatedFrontendArtifact(relatedFrontendStep(worker, "Download same-attempt preparation"), "actions/download-artifact",
			map[string]string{"name": relatedFrontendPlanArtifact, "path": "${{ runner.temp }}/frontend-related-plan/"}) ||
		!relatedFrontendArtifact(relatedFrontendStep(aggregate, "Download same-attempt preparation"), "actions/download-artifact",
			map[string]string{"name": relatedFrontendPlanArtifact, "path": "${{ runner.temp }}/frontend-related-plan/"}) ||
		!relatedFrontendWorkerEvidence(relatedFrontendStep(worker, "Upload actual worker terminal evidence")) ||
		!relatedFrontendResultDownload(relatedFrontendStep(aggregate, "Download every current related result")) {
		return false, invalid
	}
	return true, nil
}

func relatedFrontendStep(job *yaml.Node, name string) *yaml.Node {
	steps := mappingValue(job, "steps")
	if steps == nil || steps.Kind != yaml.SequenceNode {
		return nil
	}
	var found *yaml.Node
	for _, step := range steps.Content {
		if exactString(mappingValue(step, "name"), name) {
			if found != nil {
				return nil
			}
			found = step
		}
	}
	return found
}

func relatedFrontendScalar(node *yaml.Node, tag, value string) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.Tag == tag && node.Value == value
}

func relatedFrontendRun(step *yaml.Node, command string) bool {
	run := mappingValue(step, "run")
	return run != nil && run.Kind == yaml.ScalarNode && run.Tag == "!!str" && strings.TrimSpace(run.Value) == command
}

func relatedFrontendArtifact(step *yaml.Node, action string, inputs map[string]string) bool {
	uses, with := mappingValue(step, "uses"), mappingValue(step, "with")
	if uses == nil || !exactString(uses, uses.Value) || !strings.HasPrefix(uses.Value, action+"@") {
		return false
	}
	keys := make([]string, 0, len(inputs))
	for key := range inputs {
		keys = append(keys, key)
	}
	if !mappingHasExactKeys(with, keys...) {
		return false
	}
	for key, value := range inputs {
		tag := "!!str"
		if key == "retention-days" {
			tag = "!!int"
		}
		if !relatedFrontendScalar(mappingValue(with, key), tag, value) {
			return false
		}
	}
	return true
}

func relatedFrontendWorkerEvidence(step *yaml.Node) bool {
	return exactString(mappingValue(step, "if"), "always()") &&
		relatedFrontendArtifact(step, "actions/upload-artifact", map[string]string{
			"name":              relatedFrontendResultArtifact + "-${{ matrix.index }}",
			"path":              "${{ runner.temp }}/frontend-related-worker-${{ matrix.index }}/worker-${{ matrix.index }}.json",
			"if-no-files-found": "error", "retention-days": "7",
		})
}

func relatedFrontendResultDownload(step *yaml.Node) bool {
	return exactString(mappingValue(step, "if"), relatedFrontendResultsCondition) &&
		relatedFrontendArtifact(step, "actions/download-artifact", map[string]string{
			"pattern": relatedFrontendResultArtifact + "-*", "path": "${{ runner.temp }}/frontend-related-results/",
		})
}

func relatedFrontendJobCondition(job *yaml.Node, state *workflowState) bool {
	if !state.relatedFrontend || state.currentWorkflow != relatedFrontendWorkflow {
		return false
	}
	switch state.currentJob {
	case "pr-frontend-related-shards":
		return exactString(mappingValue(job, "if"), relatedFrontendWorkerCondition)
	case "pr-frontend-tests":
		return exactString(mappingValue(job, "if"), "always()")
	}
	return false
}

func relatedFrontendStepCondition(step *yaml.Node, state *workflowState) bool {
	if !state.relatedFrontend || state.currentWorkflow != relatedFrontendWorkflow {
		return false
	}
	switch state.currentJob {
	case "pr-frontend-related-shards":
		return exactString(mappingValue(step, "name"), "Upload actual worker terminal evidence") &&
			relatedFrontendWorkerEvidence(step)
	case "pr-frontend-tests":
		return exactString(mappingValue(step, "name"), "Download every current related result") &&
			relatedFrontendResultDownload(step)
	}
	return false
}
