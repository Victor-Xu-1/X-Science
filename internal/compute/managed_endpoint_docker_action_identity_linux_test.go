//go:build linux

package compute

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type managedEndpointDockerRaceStore struct{ managedEndpointWorkerGateStore }

func (*managedEndpointDockerRaceStore) AcquireManagedEndpointCleanup(context.Context, ManagedEndpointActionRef, *ManagedEndpointOwnerReceipt) (bool, error) {
	return false, nil // A failed controlled Start cannot authorize extra effects.
}

// The controlled CLI swaps the reusable name after the final successful
// preflight. No real daemon/container/service is invoked by this regression.
func TestManagedEndpointDockerControlCannotTouchSameNameReplacement(t *testing.T) {
	for _, kind := range []string{"stop", "warm_start", "cleanup"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			original := strings.Repeat("a", 64)
			replacement := strings.Repeat("b", 64)
			ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			defer ready.Close()
			parsed, err := url.Parse(ready.URL)
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(parsed.Port())
			if err != nil {
				t.Fatal(err)
			}
			flip := 2
			state := "running"
			if kind == "cleanup" {
				flip = 3
			} else if kind == "warm_start" {
				state = "exited"
			}
			cli := managedEndpointDockerIdentityRaceCLI(t, directory, original, replacement, parsed.Port(), flip)
			for name, value := range map[string]string{"name-id": original, "state": state, "inspections": "0"} {
				if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
			owner := &ManagedEndpointOwnerSpec{Version: 1, Kind: "docker",
				Docker: &ManagedEndpointDockerSpec{Context: "default", Container: "approved-service"},
				Output: ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}
			registration := ManagedEndpointRegistration{Name: "approved-service", URL: ready.URL, Port: port,
				CredentialName: "NVIDIA_API_KEY", SkillName: "using-model-endpoint", LivePath: "/health",
				StartScript: fmt.Sprintf("%q --context=default container start -- approved-service", cli),
				StopScript:  fmt.Sprintf("%q --context=default container stop --time=1 -- approved-service", cli), Owner: owner}
			instance := ManagedEndpointOwnerInstance{ID: "docker-action-race", UserID: "owner-a", Endpoint: registration.Name,
				Registration: registration, ApprovedHash: ApprovedManagedEndpointHash(registration), StartGeneration: 1,
				Identity:    ManagedEndpointRuntimeIdentity{Kind: "docker", DaemonID: "approved-daemon", ContainerID: original},
				RuntimeRoot: directory, ServiceDir: directory, WorkspacePath: filepath.Join(directory, "workspace.sqlite"),
				InitialAction: ManagedEndpointActionRef{InstanceID: "docker-action-race", Kind: "start", Generation: 1, ClaimHandle: "initial"}}
			action := ManagedEndpointAction{Ref: ManagedEndpointActionRef{InstanceID: instance.ID, Kind: "stop", Generation: 2, ClaimHandle: "current"}, Accepted: true}
			if kind == "warm_start" {
				action.Ref.Kind = "start"
			}
			output, err := newManagedEndpointOutput(filepath.Join(directory, "output"), owner.Output)
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			writer, err := os.OpenFile(filepath.Join(directory, "command-output"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			worker := &managedEndpointWorker{instance: instance, action: action, output: output, writer: writer,
				journalPath: filepath.Join(directory, "action.json"), store: &managedEndpointDockerRaceStore{managedEndpointWorkerGateStore{original: action}}}
			values := map[string]string{"HOST_PORT": strconv.Itoa(port), "SERVICE_DIR": directory}
			var receipt *ManagedEndpointOwnerReceipt
			if kind == "cleanup" {
				value := ManagedEndpointOwnerReceipt{Ref: action.Ref, Accepted: true}
				worker.cleanupDocker(instance, &value, values)
				receipt = &value
			} else {
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer cancel()
				worker.execute(ctx, action, values)
				receipt = worker.action.Receipt
			}
			if _, err := os.Stat(filepath.Join(directory, "foreign-touched")); !os.IsNotExist(err) {
				t.Errorf("lifecycle action touched the same-name foreign replacement: %v", err)
			}
			targets, err := os.ReadFile(filepath.Join(directory, "control-targets"))
			if err != nil || strings.TrimSpace(string(targets)) != original {
				t.Errorf("control was not dispatched exactly once to the immutable original ID: %q %v", targets, err)
			}
			if receipt == nil || receipt.Failure != "" || receipt.CleanupFailure != "" ||
				(kind == "warm_start" && !receipt.Readiness) ||
				(kind != "warm_start" && !receipt.HasPositiveStoppedProof(instance)) {
				t.Errorf("original owner did not settle truthfully: %+v", receipt)
			}
		})
	}
}

func managedEndpointDockerIdentityRaceCLI(t *testing.T, directory, original, replacement, port string, flip int) string {
	t.Helper()
	inspection := func(running bool) string {
		status := "exited"
		if running {
			status = "running"
		}
		value := map[string]any{
			"Id": original, "Config": map[string]any{"Labels": map[string]string{}},
			"State":           map[string]any{"Status": status, "Running": running, "Restarting": false},
			"HostConfig":      map[string]any{"LogConfig": map[string]any{"Type": "local", "Config": map[string]string{"max-size": "4k", "max-file": "2"}}},
			"NetworkSettings": map[string]any{"Ports": map[string]any{"8080/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": port}}}},
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	lines := []string{
		"#!/bin/sh", "set -eu", fmt.Sprintf("fixture=%q", directory),
		"test \"$1\" = --context=default", "shift",
		"case \"$1 $2\" in",
		"  'info --format') printf '%s\\n' approved-daemon ;;",
		"  'container ls') cat \"$fixture/name-id\" ;;",
		"  'container inspect')",
		"    n=$(cat \"$fixture/inspections\"); n=$((n+1)); printf '%s\\n' \"$n\" > \"$fixture/inspections\"",
		fmt.Sprintf("    if test \"$n\" -eq %d; then printf '%%s\\n' %s > \"$fixture/name-id\"; fi", flip, replacement),
		"    if test \"$(cat \"$fixture/state\")\" = running; then",
		"      printf '%s\\n' '" + inspection(true) + "'",
		"    else printf '%s\\n' '" + inspection(false) + "'; fi ;;",
		"  'container start'|'container stop')",
		"    operation=$2; target=; for target; do :; done",
		"    if test \"$target\" = approved-service; then target=$(cat \"$fixture/name-id\"); fi",
		"    printf '%s\\n' \"$target\" >> \"$fixture/control-targets\"",
		"    if test \"$target\" = " + original + "; then",
		"      if test \"$operation\" = start; then printf running > \"$fixture/state\"; else printf exited > \"$fixture/state\"; fi",
		"    else printf touched > \"$fixture/foreign-touched\"; fi ;;",
		"  *) exit 92 ;;", "esac", "",
	}
	path := filepath.Join(directory, "docker")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
