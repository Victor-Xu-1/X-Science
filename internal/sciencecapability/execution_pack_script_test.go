package sciencecapability

import "testing"

func TestExecutionPackScriptRejectsUnsafeLocalPaths(t *testing.T) {
	cases := []ExecutionPack{
		{Mode: "local", Script: "../executionpacks/autodock_vina.py"},
		{Mode: "local", Script: "/etc/passwd"},
		{Mode: "local", Script: ""},
		{Mode: "unavailable", Script: "executionpacks/autodock_vina.py"},
	}
	for _, pack := range cases {
		if _, _, err := ExecutionPackScript(pack); err == nil {
			t.Fatalf("unsafe execution pack %#v was accepted", pack)
		}
	}
}

func TestExecutionPackScriptLoadsBundledLocalPack(t *testing.T) {
	catalog, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var pack ExecutionPack
	for _, definition := range catalog.Capabilities {
		if definition.ID != "molecular-docking" {
			continue
		}
		for _, engine := range definition.AcceptedEngines {
			if engine.ID == "autodock-vina" {
				pack = engine.ExecutionPack
				break
			}
		}
	}
	if pack.Mode != "local" || pack.Script != "executionpacks/autodock_vina.py" {
		t.Fatalf("autodock-vina pack=%#v", pack)
	}
	raw, digest, err := ExecutionPackScript(pack)
	if err != nil || len(raw) == 0 || len(digest) != 64 {
		t.Fatalf("script=%d digest=%q err=%v", len(raw), digest, err)
	}
}
