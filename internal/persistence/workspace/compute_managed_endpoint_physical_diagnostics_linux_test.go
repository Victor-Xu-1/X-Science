//go:build linux

package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"x-science/internal/compute"
)

// Failure diagnostics do not extend execution or change assertions. The short
// independent budget is only for read-only evidence before exact cleanup; no
// credentials/Config.Env/claim token, Stop or retry is part of this snapshot.
func (fixture *managedEndpointPhysicalFixture) diagnoseDockerStart(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	instance, action, err := fixture.store.ReadManagedEndpointAction(ctx, fixture.action.Ref)
	if err != nil {
		t.Logf("Docker Start diagnostic canonical read: %v", err)
	} else {
		managedEndpointPhysicalLogAction(t, "canonical", action)
	}
	directory := filepath.Join(fixture.instance.RuntimeRoot, fixture.instance.ID)
	journalPath := filepath.Join(directory, "action.json")
	if info, statErr := os.Lstat(journalPath); statErr != nil || !info.Mode().IsRegular() || info.Size() > 256<<10 {
		t.Logf("Docker Start diagnostic journal unavailable: %v", statErr)
	} else if file, openErr := os.Open(journalPath); openErr != nil {
		t.Logf("Docker Start diagnostic journal open: %v", openErr)
	} else {
		var journal compute.ManagedEndpointAction
		decodeErr := json.NewDecoder(io.LimitReader(file, 256<<10+1)).Decode(&journal)
		_ = file.Close()
		if decodeErr != nil || journal.Ref != fixture.action.Ref {
			t.Logf("Docker Start diagnostic journal malformed/foreign: %v", decodeErr)
		} else {
			managedEndpointPhysicalLogAction(t, "worker-journal", journal)
		}
	}
	for _, name := range []string{"output-0", "output-1"} {
		text, readErr := managedEndpointPhysicalDiagnosticTail(filepath.Join(directory, "output", name), 1024)
		t.Logf("Docker Start diagnostic %s tail<=1KiB: %q read=%v", name, text, readErr)
	}
	fixture.diagnoseDockerHTTP(t, ctx)
	id := fixture.containerID
	if id == "" {
		text, readErr := managedEndpointPhysicalDiagnosticTail(filepath.Join(fixture.instance.ServiceDir, "container-id"), 128)
		if readErr != nil {
			t.Logf("Docker Start diagnostic private container-id unavailable: %v", readErr)
			return
		}
		id = strings.TrimSpace(text)
	}
	// Reuse the existing full ID/daemon/image/label/resource proof before logs.
	// Even diagnostics may not read an unrelated same-name container.
	local := *fixture
	if err == nil {
		local.instance = instance
	}
	facts, factsErr := local.dockerFacts(ctx, id)
	if factsErr != nil {
		t.Logf("Docker Start diagnostic exact container proof unavailable: %v", factsErr)
		return
	}
	t.Logf("Docker Start diagnostic owned container=%s running=%t status=%s exit=%d error=%q ports=%v", facts.ID, facts.Running, facts.Status, facts.ExitCode, managedEndpointPhysicalDiagnosticText(facts.Error, 512), facts.Ports)
	logs, logsErr := managedEndpointPhysicalCommandLimit(ctx, 2048, fixture.docker, "--context=default", "container", "logs", "--tail=12", "--", id)
	t.Logf("Docker Start diagnostic owned logs<=2KiB (partial snapshot): %q read=%v", logs, logsErr)
}

func managedEndpointPhysicalLogAction(t *testing.T, source string, action compute.ManagedEndpointAction) {
	t.Helper()
	t.Logf("Docker Start diagnostic %s instance=%s kind=%s generation=%d accepted=%t cancel=%t cleanupPending=%t receipt=%t observation=%q", source, action.Ref.InstanceID, action.Ref.Kind, action.Ref.Generation, action.Accepted, action.CancelRequested, action.CleanupPending, action.Receipt != nil, managedEndpointPhysicalDiagnosticText(action.ObservationFailure, 512))
	if receipt := action.Receipt; receipt != nil {
		var code any
		if receipt.ExitCode != nil {
			code = *receipt.ExitCode
		}
		t.Logf("Docker Start diagnostic %s receipt exited=%t code=%v noScript=%t ready=%t ownerVerified=%t stopped=%t failure=%q cleanupFailure=%q outputClosed=%t outputSequence=%d outputDropped=%d", source, receipt.ProcessExited, code, receipt.NoScript, receipt.Readiness, receipt.OwnerVerified, receipt.Stopped, managedEndpointPhysicalDiagnosticText(receipt.Failure, 512), managedEndpointPhysicalDiagnosticText(receipt.CleanupFailure, 512), receipt.OutputClosed, receipt.OutputSequence, receipt.OutputDroppedBytes)
	}
}

func (fixture *managedEndpointPhysicalFixture) diagnoseDockerHTTP(t *testing.T, ctx context.Context) {
	t.Helper()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 500 * time.Millisecond}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 750 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fixture.registration.URL+fixture.registration.LivePath, nil)
	if err != nil {
		t.Logf("Docker Start diagnostic HTTP request: %v", err)
		return
	}
	response, err := client.Do(request)
	if err != nil {
		t.Logf("Docker Start diagnostic HTTP: %v", err)
		return
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 512))
	t.Logf("Docker Start diagnostic HTTP status=%d body<=512B=%q read=%v", response.StatusCode, body, readErr)
}

func managedEndpointPhysicalDiagnosticTail(path string, limit int64) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("private diagnostic source is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if info.Size() > limit {
		if _, err := file.Seek(-limit, io.SeekEnd); err != nil {
			return "", err
		}
	}
	value, err := io.ReadAll(io.LimitReader(file, limit))
	return string(value), err
}

func managedEndpointPhysicalDiagnosticText(value string, limit int) string {
	if len(value) > limit {
		return value[:limit] + " [truncated]"
	}
	return value
}
