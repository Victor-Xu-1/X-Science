package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/skills"
)

func TestSkillLoadAfterExplicitSameNamePublicationUsesCurrentContract(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const (
		name      = "revision-aware-load-fixture"
		oldBody   = "Inspect the original synthetic contract."
		newBody   = "Inspect the revised synthetic contract."
		arguments = "retain the same requested scope"
	)
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, oldBody)); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, false)

	// Keep the same durable input claim and run across the explicit loads. A
	// publication changes the selected contract, not the user's logical input.
	run, gateway := loadedClosureFixtureGateway(fixture)
	load := func(callID string) any {
		t.Helper()
		result := executeLoadedClosureFixture(t, fixture, run, gateway, callID, name, arguments)
		return result.Value
	}
	assertContract := func(callID, want, absent string) {
		t.Helper()
		value := load(callID)
		text, ok := value.(string)
		if !ok || !strings.Contains(text, want) || strings.Contains(text, absent) {
			t.Fatalf("explicit Skill load %s did not expose its selected contract: type=%T status=%q reused=%v",
				callID, value, stringValue(mapValue(value)["status"]), mapValue(value)["reused"])
		}
	}
	assertReuse := func(callID string) {
		t.Helper()
		value := mapValue(load(callID))
		if value["reused"] != true || value["status"] != "already_loaded" {
			t.Fatalf("unchanged published contract should reuse its load receipt: status=%q reused=%v",
				stringValue(value["status"]), value["reused"])
		}
		if _, leaked := value["load_receipt"]; leaked {
			t.Fatal("compact model result leaked host-only full closure metadata")
		}
	}

	assertContract("load-original", oldBody, newBody)
	assertReuse("load-original-again")
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", newBody, oldBody); err != nil {
		t.Fatal(err)
	}
	live, found := findCatalogSkill(fixture.server.skillCatalog, name)
	if !found || !strings.Contains(live.Body, oldBody) || strings.Contains(live.Body, newBody) {
		t.Fatal("authoring a draft changed the still-published contract")
	}
	assertReuse("load-with-unpublished-draft")

	publishLoadedClosureFixture(t, fixture, name, true)
	read, err := fixture.call("host.skills.read", "local:"+name)
	if err != nil || !strings.Contains(stringValue(mapValue(read)["content"]), newBody) {
		t.Fatalf("published revised contract is not readable: %v", err)
	}
	live, found = findCatalogSkill(fixture.server.skillCatalog, name)
	if !found || !strings.Contains(live.Body, newBody) || strings.Contains(live.Body, oldBody) {
		t.Fatal("explicit publication did not update the actual runtime catalog")
	}
	assertContract("load-revised-after-publication", newBody, oldBody)
	assertReuse("load-revised-again")
}

func TestSkillLoadReceiptPreservesRenderedArgumentWhitespace(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	disableLoadedClosureFixtureMCP(t, fixture)
	const name = "argument-whitespace-load-fixture"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Before<$ARGUMENTS>After")); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, false)
	run, gateway := loadedClosureFixtureGateway(fixture)
	load := func(callID, arguments string) agentruntime.ToolResult {
		t.Helper()
		result := executeLoadedClosureFixture(t, fixture, run, gateway, callID, name, arguments)
		var executed map[string]any
		if err := json.Unmarshal(result.ExecutedArguments, &executed); err != nil || executed["args"] != arguments {
			t.Fatalf("actual gateway did not preserve raw arguments for %s: args=%#v want=%q err=%v", callID, executed["args"], arguments, err)
		}
		return result
	}
	first := load("load-raw-argument", "A")
	if text, ok := first.Value.(string); !ok || !strings.Contains(text, "Before<A>After") {
		t.Fatal("first actual Skill load omitted its rendered raw argument")
	}
	second := load("load-padded-argument", " A ")
	if text, ok := second.Value.(string); !ok || !strings.Contains(text, "Before< A >After") || strings.Contains(text, "Before<A>After") {
		value := mapValue(second.Value)
		t.Fatalf("distinct rendered argument reused the earlier contract: type=%T status=%q reused=%v", second.Value, stringValue(value["status"]), value["reused"])
	}
	if first.SkillLoadReceipt.InvocationKey == second.SkillLoadReceipt.InvocationKey {
		t.Fatal("different rendered argument bytes retained the same exact invocation key")
	}
	third := load("load-padded-argument-again", " A ")
	value := mapValue(third.Value)
	if value["reused"] != true || value["status"] != "already_loaded" ||
		third.SkillLoadReceipt.InvocationKey != second.SkillLoadReceipt.InvocationKey {
		t.Fatalf("identical raw padded argument did not reuse its committed load: status=%q reused=%v", stringValue(value["status"]), value["reused"])
	}
}

func TestSkillLoadAfterDependencyPublicationUsesCurrentClosure(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const dependency, name = "revision-dependency-fixture", "revision-leaf-fixture"
	const oldDependency, newDependency = "Original dependency instructions.", "Revised dependency instructions."
	for _, item := range []struct{ name, manifest string }{
		{dependency, kernelSkillManifestForTest(dependency, oldDependency)},
		{name, loadedClosureDependencyManifest(name, "Unchanged leaf instructions.", dependency)},
	} {
		if _, err := fixture.call("host.skills.edit", item.name, "SKILL.md", item.manifest); err != nil {
			t.Fatal(err)
		}
		publishLoadedClosureFixture(t, fixture, item.name, false)
	}
	run, gateway := loadedClosureFixtureGateway(fixture)
	first := executeLoadedClosureFixture(t, fixture, run, gateway, "load-dependency-original", name, "same scope")
	assertLoadedClosureOrder(t, first.SkillLoadReceipt, dependency, name)
	if text, ok := first.Value.(string); !ok || !strings.Contains(text, oldDependency) {
		t.Fatal("initial load omitted the published dependency contract")
	}
	if _, err := fixture.call("host.skills.edit", dependency, "SKILL.md", newDependency, oldDependency); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, dependency, true)
	second := executeLoadedClosureFixture(t, fixture, run, gateway, "load-dependency-revised", name, "same scope")
	if text, ok := second.Value.(string); !ok || !strings.Contains(text, newDependency) || strings.Contains(text, oldDependency) {
		t.Fatal("explicit load reused the unchanged leaf while its dependency changed")
	}
	if first.SkillLoadReceipt.ClosureSHA256 == second.SkillLoadReceipt.ClosureSHA256 {
		t.Fatal("dependency-only publication retained the old closure identity")
	}
}

func loadedClosureDependencyManifest(name, body string, dependencies ...string) string {
	if len(dependencies) == 0 {
		// The real parser requires non-empty sequences when dependencies are
		// declared. An instruction-only root omits that optional metadata.
		return kernelSkillManifestForTest(name, body)
	}
	return "---\nname: " + name + "\ndescription: Bounded synthetic composition.\nmetadata:\n  dependencies:\n    skills: [" +
		strings.Join(dependencies, ", ") + "]\n---\n\n" + body + "\n"
}

func assertLoadedClosureOrder(t *testing.T, receipt *agentruntime.SkillLoadReceipt, names ...string) {
	t.Helper()
	if receipt == nil || len(receipt.Contracts) != len(names) {
		t.Fatalf("actual dependency receipt has wrong closure length; want=%v receipt=%+v", names, receipt)
	}
	for index, name := range names {
		if receipt.Contracts[index].Name != name {
			t.Fatalf("actual dependency-first receipt[%d]=%q want=%q", index, receipt.Contracts[index].Name, name)
		}
	}
}

func TestSkillLoadAfterResourcePublicationUsesCurrentBundle(t *testing.T) {
	fixture := resourceLoadedClosureFixture(t)
	const name = "revision-resource-fixture"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Inspect the package reference.")); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, "references/recipe.txt", "Original resource bytes."); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, false)
	run, gateway := loadedClosureFixtureGateway(fixture)
	first := executeLoadedClosureFixture(t, fixture, run, gateway, "load-resource-original", name, "same scope")
	if _, err := fixture.call("host.skills.edit", name, "references/recipe.txt", "Revised resource bytes.", "Original resource bytes."); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, true)
	second := executeLoadedClosureFixture(t, fixture, run, gateway, "load-resource-revised", name, "same scope")
	if _, ok := second.Value.(string); !ok {
		t.Fatal("resource-only publication was treated as an already-loaded package")
	}
	oldIdentity, newIdentity := first.SkillLoadReceipt.Contracts[0], second.SkillLoadReceipt.Contracts[0]
	if oldIdentity.ContractSHA256 != newIdentity.ContractSHA256 || oldIdentity.BundleSHA256 == newIdentity.BundleSHA256 ||
		first.SkillLoadReceipt.InvocationKey == second.SkillLoadReceipt.InvocationKey {
		t.Fatal("resource-only publication did not retain instruction identity and change package identity")
	}
}

func loadedClosureFixtureGateway(fixture *kernelSkillDraftFixture) (*sessionRunnerChatRun, serverAgentRuntimeToolGateway) {
	run := &sessionRunnerChatRun{
		SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt), ClaimToken: fixture.claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim},
	}
	return run, serverAgentRuntimeToolGateway{
		server: fixture.server, sessionID: fixture.stream.SessionID,
		taskRun: run, allowedTools: []string{"skill"},
	}
}

func executeLoadedClosureFixture(
	t *testing.T,
	fixture *kernelSkillDraftFixture,
	run *sessionRunnerChatRun,
	gateway serverAgentRuntimeToolGateway,
	callID, name, arguments string,
	filters ...string,
) agentruntime.ToolResult {
	t.Helper()
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	input := map[string]any{"skill": name, "args": arguments}
	if len(filters) > 0 {
		input["filter"] = filters[0]
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	options := SessionRunnerChatOptions{SessionID: run.SessionID, RunnerID: run.Transcript.Claim.RunnerID}
	if err := fixture.server.checkpointSessionRunnerToolEvent(ctx, options, run, agentruntime.Event{
		Type: agentruntime.EventToolStarted, ToolName: "skill", ToolCallID: callID, Arguments: string(encoded),
	}); err != nil {
		t.Fatalf("start Skill call: %v", err)
	}
	result, err := gateway.Execute(ctx, agentruntime.ToolCall{ID: callID, Name: "skill", Arguments: encoded})
	if err != nil || !result.SkillLoadReceipt.BoundToCall(callID) {
		value := mapValue(result.Value)
		t.Fatalf("explicit Skill load %s: receipt=%v status=%q code=%q message=%q error=%q err=%v",
			callID, result.SkillLoadReceipt != nil, stringValue(value["status"]), stringValue(value["code"]),
			stringValue(value["message"]), stringValue(value["error"]), err)
	}
	if err := completeLoadedClosureFixture(ctx, fixture, options, run, callID, encoded, result); err != nil {
		t.Fatalf("complete Skill load %s: %v", callID, err)
	}
	return result
}

func completeLoadedClosureFixture(
	ctx context.Context,
	fixture *kernelSkillDraftFixture,
	options SessionRunnerChatOptions,
	run *sessionRunnerChatRun,
	callID string,
	arguments json.RawMessage,
	result agentruntime.ToolResult,
) error {
	encoded, err := json.Marshal(result.Value)
	if err != nil {
		return err
	}
	return fixture.server.checkpointSessionRunnerToolEvent(ctx, options, run, agentruntime.Event{
		Type: agentruntime.EventToolCompleted, ToolName: "skill", ToolCallID: callID,
		Arguments: string(arguments), ExecutedArguments: string(result.ExecutedArguments), Result: string(encoded),
		SkillLoadReceipt: result.SkillLoadReceipt,
	})
}

func selectedLoadedClosureFixture(t *testing.T, fixture *kernelSkillDraftFixture, names ...string) runtimeSkillClosureSnapshot {
	t.Helper()
	selected := make([]skills.Skill, 0, len(names))
	for _, name := range names {
		skill, found := findCatalogSkill(fixture.server.skillCatalog, name)
		if !found {
			t.Fatalf("published fixture Skill %s not found", name)
		}
		selected = append(selected, skill)
	}
	closure, err := fixture.server.expandRuntimeSkillDependencies(selected, nil, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.server.captureRuntimeSkillClosureSnapshot(closure)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func publishLoadedClosureFixture(t *testing.T, fixture *kernelSkillDraftFixture, name string, overwrite bool) {
	t.Helper()
	validated, err := fixture.call("host.skills.validate", name)
	if err != nil || mapValue(validated)["valid"] != true {
		report, _ := json.Marshal(validated)
		t.Fatalf("validate synthetic Skill draft %q: report=%s err=%v", name, report, err)
	}
	published, err := fixture.call("host.skills.publish", name, overwrite)
	if err != nil || mapValue(published)["status"] != "published" {
		t.Fatalf("explicitly publish synthetic Skill draft: status=%q err=%v", stringValue(mapValue(published)["status"]), err)
	}
}

func disableLoadedClosureFixtureMCP(t *testing.T, fixture *kernelSkillDraftFixture) {
	t.Helper()
	if err := disableBundledMCPForLocalFixture(context.Background(), fixture.server, fixture.stream.OwnerID); err != nil {
		t.Fatal(err)
	}
}
