package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"synon-go/internal/buildinfo"
	"synon-go/internal/capabilities"
	kernelruntime "synon-go/internal/kernel"
	workspace "synon-go/internal/persistence/workspace"
	pluginhost "synon-go/internal/plugins/host"
)

func TestCurrentNativeBrandOutputsKeepMachineCompatibility(t *testing.T) {
	name := buildinfo.Release().Name
	if got := localIdentityEmail("Local User"); got != "local-user@local."+buildinfo.Release().MachineSlug {
		t.Fatalf("synthetic local display email did not use the root identity: %q", got)
	}
	plugin, found := pluginhost.NewDefault().Get("synon")
	if !found || plugin.ID != "synon" || plugin.Name != name || plugin.Mount != "/api/plugins/synon" ||
		plugin.PluginFormat != "synon_agent_native" || plugin.Server.ContextEntry != "builtin:synon-context" {
		t.Fatalf("plugin branding changed its identity contract: %#v", plugin)
	}
	if !strings.Contains(plugin.Modules[1].Label, name) || !strings.Contains(plugin.Description, name) {
		t.Fatalf("plugin display label did not use current branding: %#v", plugin)
	}
	encoded, err := json.Marshal(capabilities.CompactSynon())
	if err != nil || !strings.Contains(string(encoded), name+" Link") || strings.Contains(string(encoded), "Synon Link") {
		t.Fatalf("native capability display retains historical branding: %s %v", encoded, err)
	}
	if got := externalIdentityFallbackName("unrecognized"); got != name+" user" {
		t.Fatalf("new account fallback=%q", got)
	}
	if command := strings.Join(windowsFolderPickerArguments(), " "); !strings.Contains(command, name+" access") || strings.Contains(command, "Synon access") {
		t.Fatal("native picker still uses historical display branding")
	}
	for _, reason := range []string{name + " kernel confinement is unavailable on this platform", "Synon kernel confinement is unavailable on this platform"} {
		if got := kernelruntime.DiagnoseConfinementEvidence(kernelruntime.ConfinementEvidence{Reason: reason}); got.Code != "kernel_confinement_unsupported" {
			t.Fatalf("current/historical reason lost its closed diagnostic: %#v", got)
		}
	}
	path := filepath.Join(t.TempDir(), "synthetic-browser-extension.zip")
	if err := os.WriteFile(path, []byte("owned synthetic archive bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	(&Server{linkPackagePath: path}).handleSynonLinkDownload(response, httptest.NewRequest(http.MethodGet, "/api/synon-link/download", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Disposition") != `attachment; filename="x-science-browser-extension.zip"` || response.Body.String() != "owned synthetic archive bytes" {
		t.Fatalf("download display changed underlying package bytes: %d %#v", response.Code, response.Header())
	}
}

func TestCurrentReproducibilityReportsPreserveSuppliedData(t *testing.T) {
	name := buildinfo.Release().Name
	userID := "Synon user-supplied artifact identity"
	script := scriptBundleREADME(workspace.Artifact{ID: userID}, workspace.ArtifactVersion{ID: "version"}, workspace.ArtifactLineageRecord{}, "python")
	if !strings.HasPrefix(script, "# "+name+" script reproducibility bundle\n") || !strings.Contains(script, userID) {
		t.Fatalf("script report altered current branding or supplied data: %q", script)
	}
	session := sessionBundleREADME(workspace.CompatibilitySessionExport{RootFrameID: userID}, "full")
	if !strings.HasPrefix(session, "# "+name+" reproducibility bundle\n") || !strings.Contains(session, userID) {
		t.Fatalf("session report altered current branding or supplied data: %q", session)
	}
	app, _, _, versionID, _ := exportFixture(t)
	request := httptest.NewRequest(http.MethodGet, "/api/artifacts/"+versionID+"/script-bundle", nil)
	request.Header.Set("X-Synon-User-Id", "user-1")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("owned script export failed: %d %s", response.Code, response.Body.String())
	}
	entries := zipEntryContents(t, response.Body.Bytes())
	if !strings.HasPrefix(string(entries["README.md"]), "# "+name+" script reproducibility bundle\n") || string(entries["inputs/data.json"]) != `{"ok":true}` {
		t.Fatalf("actual ZIP display/input mismatch: %#v", entries)
	}
}
