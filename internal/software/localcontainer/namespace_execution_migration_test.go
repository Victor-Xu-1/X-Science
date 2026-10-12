package localcontainer

import (
	"context"
	"errors"
	"testing"
)

func TestNamespaceContainerHistoricalExecutionWithoutActualReceiptNeverCreates(t *testing.T) {
	docker := &executionDockerRunner{}
	manager, environment := testContainerEnvironment(t, docker)
	_, err := manager.RunExecution(context.Background(), make(chan struct{}), environment, ExecutionRequest{
		ExecutionID: "execution-1", ToolName: "bash", Code: "echo original", Workspace: t.TempDir(), BeforeNamespaceCutover: true,
	})
	if !errors.Is(err, ErrExecutionOutcomeUnconfirmed) || docker.created || len(docker.createArgs) != 0 {
		t.Fatalf("historical execution duplicated: %v %#v", err, docker.createArgs)
	}
}

func TestNamespaceContainerMissingPersistedRuntimeNeverCreatesReplacement(t *testing.T) {
	docker := &executionDockerRunner{}
	manager, environment := testContainerEnvironment(t, docker)
	identity := RuntimeIdentity{ExecutionID: "execution-1", ContainerID: "actual-recorded-id", ImageID: environment.ImageID, Environment: environment.Name, EnvironmentGeneration: environment.HostGeneration}
	_, err := manager.RunExecution(context.Background(), make(chan struct{}), environment, ExecutionRequest{
		ExecutionID: "execution-1", ToolName: "bash", Code: "echo original", Workspace: t.TempDir(), Identity: &identity,
	})
	if !errors.Is(err, ErrExecutionOutcomeUnconfirmed) || docker.created {
		t.Fatalf("missing actual runtime duplicated: %v", err)
	}
}

func TestNamespaceContainerPersistsActualIdentityBeforeStartOrDetach(t *testing.T) {
	docker := &executionDockerRunner{remainRunning: true}
	manager, environment := testContainerEnvironment(t, docker)
	detach := make(chan struct{})
	recorded := false
	_, err := manager.RunExecution(context.Background(), detach, environment, ExecutionRequest{
		ExecutionID: "execution-1", ToolName: "bash", Code: "echo original", Workspace: t.TempDir(),
		RecordIdentity: func(identity RuntimeIdentity) error {
			if docker.started || identity.ContainerID != "container-id" || identity.ExecutionID != "execution-1" || identity.ImageID != environment.ImageID || identity.EnvironmentGeneration != environment.HostGeneration {
				t.Fatal("incorrect or late runtime receipt")
			}
			recorded = true
			close(detach)
			return nil
		},
	})
	if !recorded || !errors.Is(err, ErrExecutorDetached) || docker.stopCount != 0 {
		t.Fatalf("detachment lost durable runtime: recorded=%v err=%v", recorded, err)
	}
}

func TestNamespaceContainerReceiptFailureDoesNotStartWork(t *testing.T) {
	docker := &executionDockerRunner{}
	manager, environment := testContainerEnvironment(t, docker)
	_, err := manager.RunExecution(context.Background(), make(chan struct{}), environment, ExecutionRequest{
		ExecutionID: "execution-1", ToolName: "bash", Code: "echo original", Workspace: t.TempDir(),
		RecordIdentity: func(RuntimeIdentity) error { return errors.New("receipt storage unavailable") },
	})
	if !errors.Is(err, ErrExecutionOutcomeUnconfirmed) || docker.started {
		t.Fatalf("unrecorded runtime started: %v", err)
	}
}

func TestNamespaceContainerHistoricalLabelsRequireActualCutoffBoundID(t *testing.T) {
	environment := Environment{Name: "swc-00112233445566778899aabb", ImageID: testImageID, HostGeneration: "original-generation"}
	identity := RuntimeIdentity{ExecutionID: "execution-original", ContainerID: "actual-old-id", Environment: environment.Name, ImageID: testImageID, EnvironmentGeneration: environment.HostGeneration}
	container := dockerContainer{ID: identity.ContainerID, Image: testImageID}
	container.Config.Labels = map[string]string{"com.synonbiomed.managed": "true", "com.synonbiomed.execution": identity.ExecutionID, "com.synonbiomed.environment": identity.Environment}
	request := ExecutionRequest{ExecutionID: identity.ExecutionID, Identity: &identity, BeforeNamespaceCutover: true}
	if err := validateContainerRuntimeReceipt(container, environment, request); err != nil {
		t.Fatal(err)
	}
	request.BeforeNamespaceCutover = false
	if err := validateContainerRuntimeReceipt(container, environment, request); err == nil {
		t.Fatal("old labels accepted without cutoff proof")
	}
	request.BeforeNamespaceCutover = true
	container.ID = "different-actual-id"
	if err := validateContainerRuntimeReceipt(container, environment, request); err == nil {
		t.Fatal("different actual ID accepted")
	}
}
