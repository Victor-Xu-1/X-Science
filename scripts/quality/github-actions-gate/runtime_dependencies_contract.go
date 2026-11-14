package main

import (
	"strings"

	"gopkg.in/yaml.v3"
)

const prRuntimeDependencyCondition = "${{ toJSON(matrix.has_go_packages) != 'false' }}"

const runtimeDependencySetupRun = `sudo apt-get update
sudo apt-get install --yes bubblewrap poppler-utils
command -v pdfinfo pdftotext
sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0
bwrap --unshare-user --disable-userns --unshare-ipc --unshare-uts \
  --new-session --die-with-parent --unshare-net --unshare-pid \
  --ro-bind / / --proc /proc --dev /dev /bin/true
runtime_python="$(command -v python3)"
sudo "$runtime_python" -m pip install --break-system-packages --ignore-installed \
  --requirement assets/optional/mcp-servers/bio-tools/requirements-test.txt
"$runtime_python" -c 'import anyio, httpx, jsonschema, lxml, mcp, pandas, requests, urllib3; from PIL import Image'`

// An explicit empty Go selection may omit only this dependency preparation.
// Binding the run block prevents a renamed required test from borrowing it.
func prRuntimeDependencyStepCondition(step *yaml.Node, state *workflowState) bool {
	if state == nil || state.currentWorkflow != ".github/workflows/quality-pr.yml" || state.currentJob != "pr-go-shards" {
		return false
	}
	if !mappingHasExactKeys(step, "name", "if", "run") ||
		!exactString(mappingValue(step, "name"), "Set up runtime test dependencies") ||
		!exactString(mappingValue(step, "if"), prRuntimeDependencyCondition) {
		return false
	}
	run := mappingValue(step, "run")
	return run != nil && run.Kind == yaml.ScalarNode && run.Tag == "!!str" &&
		strings.TrimSpace(run.Value) == runtimeDependencySetupRun
}
