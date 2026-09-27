package sciencecapability

import "testing"

func TestDocumentedInputContract(t *testing.T) {
	valid := func() ExecutionPack {
		return ExecutionPack{
			Mode: "local", Inputs: []ExecutionInput{{Kind: "source", Argument: "--source"}},
			Parameters: []ExecutionParameter{
				{Name: "point", Argument: "--point", Type: "number", Evidence: "resolved-user-input", EvidenceGroup: "control-point"},
				{Name: "evidence", Argument: "--evidence", Type: "string"},
			},
			DocumentedInputs: []ExecutionDocumentedInput{{EvidenceGroup: "control-point", Argument: "--evidence", InputKind: "source"}},
		}
	}
	if err := validateDocumentedInputs(valid()); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ExecutionPack){
		"missing source":   func(p *ExecutionPack) { p.DocumentedInputs[0].InputKind = "absent" },
		"missing values":   func(p *ExecutionPack) { p.DocumentedInputs[0].EvidenceGroup = "unknown" },
		"missing argument": func(p *ExecutionPack) { p.DocumentedInputs[0].Argument = "--unknown" },
		"wrong type":       func(p *ExecutionPack) { p.Parameters[1].Type = "number" },
		"default evidence": func(p *ExecutionPack) { p.Parameters[1].Default = "manufactured.json" },
		"mixed authority": func(p *ExecutionPack) {
			p.Parameters[1].InputEvidence = &ExecutionInputEvidence{EvidenceGroup: "control-point", OutputKind: "receipt"}
		},
		"duplicates": func(p *ExecutionPack) { p.DocumentedInputs = append(p.DocumentedInputs, p.DocumentedInputs[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			pack := valid()
			mutate(&pack)
			if validateDocumentedInputs(pack) == nil {
				t.Fatal("invalid route accepted")
			}
		})
	}
}
