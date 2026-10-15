package compute

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"text/template"
)

func TestManagedEndpointDockerInspectProjectionRendersStrictJSON(t *testing.T) {
	// Render the exact production Go-template, not a separately hand-assembled
	// valid JSON response. Match Docker's json function with encoding/json.
	projection, err := template.New("actual-docker-inspect").Option("missingkey=error").Funcs(template.FuncMap{
		"json": func(value any) (string, error) {
			encoded, err := json.Marshal(value)
			return string(encoded), err
		},
	}).Parse(managedEndpointDockerInspectProjection)
	if err != nil {
		t.Fatal(err)
	}
	id, instance, user, approval := strings.Repeat("a", 64), "fixture-instance", "owner with \"quotes\"", strings.Repeat("b", 64)
	facts := map[string]any{
		"Id": id,
		"Config": map[string]any{
			"Labels": map[string]string{"ai.x-science.endpoint-instance": instance, "ai.x-science.endpoint-user": user, "ai.x-science.endpoint-approval": approval, "unrelated": "MUST_NOT_PROJECT_OTHER_LABEL"},
			"Env":    []string{"MUST_NOT_PROJECT_ENV"},
		},
		"State":      map[string]any{"Status": "running", "Running": true, "Restarting": false},
		"HostConfig": map[string]any{"LogConfig": map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "64k", "max-file": "2"}}},
		"NetworkSettings": map[string]any{"Ports": map[string]any{
			"8080/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": "25001"}},
		}},
	}
	var rendered bytes.Buffer
	if err := projection.Execute(&rendered, facts); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(rendered.Bytes()))
	decoder.DisallowUnknownFields()
	var actual managedEndpointDockerInspection
	if err := decoder.Decode(&actual); err != nil {
		t.Fatalf("actual Docker Go-template did not render strict inspection JSON: %v", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("actual Docker projection has trailing JSON: %v", err)
	}
	if actual.ID != id || !actual.State.Running || actual.State.Restarting || actual.State.Status != "running" ||
		len(actual.Config.Labels) != 3 || actual.Config.Labels["ai.x-science.endpoint-instance"] != instance ||
		actual.Config.Labels["ai.x-science.endpoint-user"] != user || actual.Config.Labels["ai.x-science.endpoint-approval"] != approval {
		t.Fatalf("actual projection lost its owner/state fields: %#v", actual)
	}
	if err := validateManagedEndpointDockerLogging(actual); err != nil {
		t.Fatalf("actual projection lost its finite logging fields: %v", err)
	}
	if !managedEndpointDockerPortMatches(ManagedEndpointRegistration{URL: "http://127.0.0.1:25001"}, actual) {
		t.Fatal("actual projection lost the sibling NetworkSettings port binding")
	}
	if strings.Contains(rendered.String(), "MUST_NOT_PROJECT") {
		t.Fatal("actual owner projection disclosed environment or unrelated labels")
	}
}
