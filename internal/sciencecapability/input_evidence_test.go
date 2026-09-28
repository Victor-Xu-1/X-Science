package sciencecapability

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestExecutionInputEvidenceContract(t *testing.T) {
	base, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*ExecutionPack){
		"missing resolver":      func(p *ExecutionPack) { p.EvidenceResolvers = nil },
		"unknown output":        func(p *ExecutionPack) { firstInputEvidence(p).OutputKind = "unknown-output" },
		"unknown group":         func(p *ExecutionPack) { firstInputEvidence(p).EvidenceGroup = "unknown-group" },
		"unknown lineage input": func(p *ExecutionPack) { p.InputLineage[0].InputKind = "unknown-input" },
		"non JSON lineage":      func(p *ExecutionPack) { p.InputLineage[0].OutputKind = "execution-log" },
		"bad pointer":           func(p *ExecutionPack) { p.InputLineage[0].SHA256Pointer = "/bad~9" },
		"missing pointer":       func(p *ExecutionPack) { p.InputLineage[0].SHA256Pointer = "" },
		"duplicate lineage":     func(p *ExecutionPack) { p.InputLineage = append(p.InputLineage, p.InputLineage[0]) },
		"oversized lineage": func(p *ExecutionPack) {
			for len(p.InputLineage) <= 64 {
				p.InputLineage = append(p.InputLineage, p.InputLineage[0])
			}
		},
		"competing value authority": func(p *ExecutionPack) {
			for i := range p.Parameters {
				if p.Parameters[i].InputEvidence != nil {
					p.Parameters[i].Evidence = "runtime-response-language"
					break
				}
			}
		},
		"default evidence file": func(p *ExecutionPack) {
			for i := range p.Parameters {
				if p.Parameters[i].InputEvidence != nil {
					p.Parameters[i].Default = "guessed.json"
					break
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			var catalog Catalog
			if err := json.Unmarshal(raw, &catalog); err != nil {
				t.Fatal(err)
			}
			changed := false
			for i := range catalog.Capabilities {
				for j := range catalog.Capabilities[i].AcceptedEngines {
					p := &catalog.Capabilities[i].AcceptedEngines[j].ExecutionPack
					if len(p.InputLineage) > 0 {
						change(p)
						changed = true
					}
				}
			}
			if !changed {
				t.Fatal("no production input contract exercised")
			}
			mutated, _ := json.Marshal(catalog)
			if _, err := Decode(bytes.NewReader(mutated)); err == nil {
				t.Fatal("invalid evidence contract admitted")
			}
		})
	}
	if _, err := Decode(bytes.NewReader(raw)); err != nil {
		t.Fatalf("production contract cannot roundtrip: %v", err)
	}
}

func firstInputEvidence(p *ExecutionPack) *ExecutionInputEvidence {
	for i := range p.Parameters {
		if p.Parameters[i].InputEvidence != nil {
			return p.Parameters[i].InputEvidence
		}
	}
	panic("test pack has no input evidence")
}
