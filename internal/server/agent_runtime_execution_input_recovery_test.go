package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	kernelruntime "synon-go/internal/kernel"
	workspace "synon-go/internal/persistence/workspace"
	"synon-go/internal/sciencecapability"
)

func inputAuthorityReceipt(t *testing.T, f *agentSaveArtifactsFixture, root, id, status, kernelOverride string) {
	t.Helper()
	access := f.identity.access
	id += "-" + access.Frame.ID
	kernelID, err := kernelruntime.StableSessionID(kernelruntime.SessionSpec{
		OwnerID: access.UserID, ProjectID: access.Frame.ProjectID, FrameID: access.Frame.ID,
		FrameIncarnationID: access.Frame.IncarnationID, RootFrameID: access.Frame.RootFrameID,
		RootFrameIncarnationID: access.RootFrameIncarnationID, AgentName: access.Frame.AgentName,
		KernelKind: "bash", Language: "python", Environment: "python", WorkspaceDir: f.projectPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if kernelOverride != "" {
		kernelID = kernelOverride
	}
	data, err := os.ReadFile(filepath.Join(f.projectPath, "dataset.txt"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	proof, _ := json.Marshal(map[string]any{"overall_pass": true, "inputs": map[string]string{"dataset": hex.EncodeToString(digest[:])}, "run": id})
	contents := map[string]string{
		managedExecutionOutputOwnershipMarker: `{"schema":"synon.execution-pack-output-owner.v1","execution_pack_id":"analysis.producer"}`,
		"value.txt":                           "fixture-derived-" + id, "proof.json": string(proof),
	}
	var writes []kernelruntime.FileWrite
	for name, content := range contents {
		relative := filepath.Join(root, name)
		path := filepath.Join(f.projectPath, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		sha := sha256.Sum256([]byte(content))
		writes = append(writes, kernelruntime.FileWrite{Path: relative, SHA256: hex.EncodeToString(sha[:])})
	}
	_, err = f.store.SaveExecutionLog(workspace.SaveExecutionLogInput{
		Record: workspace.ExecutionLogRecord{ID: id, FrameID: access.Frame.ID, KernelID: kernelID,
			KernelKind: "bash", CondaEnv: "python", Language: "python", Origin: "agent", ExitStatus: status,
			Source: `python3 "` + filepath.Join(f.projectPath, ".synon/runtime/skills/prepare-data-fixture/scripts/prepare.py") + `"`, FilesWritten: writes},
		ExpectedOwnerID: access.UserID, ExpectedProjectID: access.Frame.ProjectID,
		ExpectedFrameIncarnationID: access.Frame.IncarnationID, ExpectedRootFrameIncarnationID: access.RootFrameIncarnationID,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExecutionInputAuthorityRecoveryBoundaries(t *testing.T) {
	// Share only the migrated schema. Every case owns a distinct Frame and
	// workspace, keeping isolation real without repeatedly testing migrations.
	base := newAgentSaveArtifactsFixture(t)
	for _, scenario := range []string{"valid", "renamed", "failed", "foreign-kernel", "foreign-frame", "tampered", "changed-input", "mixed-execution", "missing-output", "unsafe-path", "abbreviation", "duplicate", "incomplete", "ordinary-input", "canceled", "materialization-unavailable", "selected-other"} {
		t.Run(scenario, func(t *testing.T) {
			f := newExecutionInputFrameFixture(t, base)
			command := executionInputAuthorityFixture(t, f.server, f.projectPath)
			status, kernelID := "ok", ""
			if scenario == "failed" {
				status = "error"
			}
			if scenario == "foreign-kernel" {
				kernelID = "kernel-another-task"
			}
			inputAuthorityReceipt(t, f, "results", "producer-one", status, kernelID)
			write := func(relative, content string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(f.projectPath, relative), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "renamed":
				bytes, err := os.ReadFile(filepath.Join(f.projectPath, "results/value.txt"))
				if err != nil {
					t.Fatal(err)
				}
				write("renamed.any", string(bytes))
				command = strings.ReplaceAll(command, "results/value.txt", "renamed.any")
			case "tampered":
				write("results/value.txt", "replacement")
			case "changed-input":
				write("dataset.txt", "other dataset")
			case "mixed-execution":
				inputAuthorityReceipt(t, f, "other", "producer-two", "ok", "")
				command = strings.ReplaceAll(command, "results/proof.json", "other/proof.json")
			case "missing-output":
				if err := os.Remove(filepath.Join(f.projectPath, "results/value.txt")); err != nil {
					t.Fatal(err)
				}
			case "unsafe-path":
				foreign := filepath.Join(t.TempDir(), "foreign.txt")
				if err := os.WriteFile(foreign, []byte("foreign"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(foreign, filepath.Join(f.projectPath, "foreign")); err != nil {
					t.Fatal(err)
				}
				command = strings.ReplaceAll(command, "results/value.txt", "foreign")
			case "abbreviation":
				command = strings.ReplaceAll(command, "--derived", "--der")
			case "duplicate":
				command += " --derived results/value.txt"
			case "incomplete":
				command = strings.ReplaceAll(command, " --receipt results/proof.json", "")
			case "ordinary-input":
				command = strings.Split(command, " --derived")[0]
			case "materialization-unavailable":
				f.server.fileRoot = ""
			case "foreign-frame":
				other := newExecutionInputFrameFixture(t, base)
				f.identity.access = other.identity.access
			}
			ctx := context.Background()
			if scenario == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if scenario == "selected-other" {
				run := &sessionRunnerChatRun{}
				run.setSelectedEvidenceResolvers(sciencecapability.ExecutionEvidenceResolver{EvidenceGroup: "analysis-input", Skill: "other-skill", Implementation: "other-engine"})
				ctx = context.WithValue(ctx, transcriptRunnerChatRunContextKey{}, run)
			}
			input := map[string]any{"command": command}
			normalized, boundary, err := f.server.bindManagedExecutionInputs(ctx, f.identity.access, f.projectPath, "", "bash", input)
			if scenario == "canceled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
				return
			}
			allowed := scenario == "valid" || scenario == "renamed" || scenario == "ordinary-input"
			if err != nil || (boundary == nil) != allowed {
				t.Fatalf("boundary=%#v error=%v", boundary, err)
			}
			if !allowed && (boundary["executed"] != false || boundary["preflight"] != true) {
				t.Fatalf("lost nonexecution semantics: %#v", boundary)
			}
			if scenario == "materialization-unavailable" && (boundary["reason"] != "materialization_unavailable" || boundary["retryable"] != true) {
				t.Fatalf("infrastructure failure lost recovery classification: %#v", boundary)
			}
			if allowed && scenario != "ordinary-input" && !strings.Contains(stringValue(normalized["command"]), ".synon-artifacts") {
				t.Fatal("consumer not pinned to immutable evidence")
			}
			if input["command"] != command {
				t.Fatal("original call was mutated")
			}
		})
	}
}

func newExecutionInputFrameFixture(t *testing.T, base *agentSaveArtifactsFixture) *agentSaveArtifactsFixture {
	t.Helper()
	frame, err := base.store.CreateFrame(workspace.CreateFrameInput{
		ID: uuid.NewString(), ProjectID: base.identity.access.Frame.ProjectID,
		AgentName: "OPERON", Status: "processing", ConversationType: "agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	access, found, err := base.store.GetKernelFrameAccessContext(context.Background(), frame.ID)
	if err != nil || !found {
		t.Fatalf("access: %v %v", found, err)
	}
	root := t.TempDir()
	return &agentSaveArtifactsFixture{
		store: base.store, databasePath: base.databasePath, projectPath: root,
		server:   &Server{workspaceStore: base.store, fileRoot: base.server.fileRoot},
		identity: &agentKernelContext{access: access, workspaceDir: root},
	}
}

func TestExecutionInputAuthoritySQLiteReopenAndConcurrentReplay(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	command := executionInputAuthorityFixture(t, f.server, f.projectPath)
	inputAuthorityReceipt(t, f, "results", "producer-one", "ok", "")
	input := map[string]any{"command": command}
	reopened, err := workspace.Open(f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	server := &Server{workspaceStore: reopened, scienceCapabilities: f.server.scienceCapabilities, fileRoot: f.server.fileRoot}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for replay := 0; replay < 12; replay++ {
				_, boundary, err := server.bindManagedExecutionInputs(context.Background(), f.identity.access, f.projectPath, "", "bash", input)
				if err != nil || boundary != nil {
					t.Errorf("reconstructed replay: %#v %v", boundary, err)
					return
				}
			}
		}()
	}
	group.Wait()
}
