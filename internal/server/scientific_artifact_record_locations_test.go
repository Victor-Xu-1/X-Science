package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestScientificArtifactRepairLocatesInvalidRecordsWithoutDroppingValidTail(t *testing.T) {
	validate := scientificArtifactStreamTestValidator(t)
	for _, test := range []struct {
		name, format, body string
		record, line       int
	}{
		{"smiles_with_header_and_comments", "smi", "# library\n\nsmiles name\nCCO first\nnot_a_molecule broken\n# comment\nCC third\n", 2, 5},
		{"sdf_middle_record", "sdf", validServerEthanolSDF + strings.Replace(validServerEthanolSDF, "  3  2", "  0  0", 1) + validServerEthanolSDF, 2, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := validate(context.Background(), strings.NewReader(test.body), test.format)
			if !errors.Is(err, errInvalidScientificArtifact) || result.InvalidRecordCount != 1 || result.ParsedCount != 2 {
				t.Fatalf("fixture must retain valid records on both sides: %#v %v", result, err)
			}
			failure := agentSaveArtifactFailure("input."+test.format, err)
			raw, _ := json.Marshal(failure)
			var feedback map[string]any
			if err := json.Unmarshal(raw, &feedback); err != nil {
				t.Fatal(err)
			}
			locations := anySliceValue(feedback["validation_invalid_record_locations"])
			if len(locations) != 1 {
				t.Fatalf("repair cannot locate the actual rejected record: %s", raw)
			}
			location := mapValue(locations[0])
			line, _ := location["line"].(float64)
			if location["record"] != float64(test.record) || line != float64(test.line) {
				t.Fatalf("repair cannot locate the actual rejected record: %s", raw)
			}
			if strings.Contains(string(raw), "not_a_molecule") || strings.Contains(string(raw), "RDKit failed") {
				t.Fatalf("feedback leaked input or stderr: %s", raw)
			}
		})
	}
}

func TestScientificArtifactRepairLocationsAreBoundedWithoutStoppingValidation(t *testing.T) {
	validate := scientificArtifactStreamTestValidator(t)
	result, err := validate(context.Background(), strings.NewReader(strings.Repeat("invalid_entry\n", 100)+"CCO valid_tail\n"), "smi")
	if !errors.Is(err, errInvalidScientificArtifact) || result.InvalidRecordCount != 100 || result.ParsedCount != 1 {
		t.Fatalf("bounded diagnosis stopped validating the complete stream: %#v %v", result, err)
	}
	failure := agentSaveArtifactFailure("input.smi", err)
	raw, _ := json.Marshal(failure)
	var feedback map[string]any
	_ = json.Unmarshal(raw, &feedback)
	if len(anySliceValue(feedback["validation_invalid_record_locations"])) != 32 || feedback["validation_locations_truncated"] != true {
		t.Fatalf("missing bounded and explicitly partial repair locations: %s", raw)
	}
}

func TestScientificArtifactRepairLocationContractRejectsUntrustedPositions(t *testing.T) {
	const prefix = `{"schemaVersion":2,"format":"smi","ok":false,"code":"invalid_smiles_records","delimiterCount":40,"supplierRecordCount":40,"parsedCount":0,"invalidRecordCount":40,"atomCountRecords":40,"terminalDelimiter":true,"rdkitVersion":"2024.03.5","invalidRecordLocations":`
	valid := prefix + `[{"record":2,"line":5},{"record":3,"line":7}]}`
	if _, err := decodeScientificArtifactValidation(strings.NewReader(valid), "smi", "2024.03.5"); err != nil {
		t.Fatal(err)
	}
	for _, locations := range []string{
		`[{"record":0,"line":1}]`,
		`[{"record":41,"line":50}]`,
		`[{"record":2,"line":1}]`,
		`[{"record":2}]`,
		`[{"record":2,"line":5},{"record":2,"line":6}]`,
		`[{"record":2,"line":5},{"record":3,"line":4}]`,
		`[{"record":2,"line":5,"raw":"untrusted input"}]`,
	} {
		if _, err := decodeScientificArtifactValidation(strings.NewReader(prefix+locations+"}"), "smi", "2024.03.5"); err == nil {
			t.Fatalf("untrusted location accepted: %s", locations)
		}
	}
	locations := make([]map[string]int, 33)
	for index := range locations {
		locations[index] = map[string]int{"record": index + 1, "line": index + 1}
	}
	raw, _ := json.Marshal(locations)
	if _, err := decodeScientificArtifactValidation(strings.NewReader(prefix+string(raw)+"}"), "smi", "2024.03.5"); err == nil {
		t.Fatal("unbounded record-location payload accepted")
	}
	for _, malformed := range []string{
		strings.Replace(valid, `"ok":false`, `"ok":true`, 1),
		strings.Replace(valid, `"invalidRecordCount":40`, `"invalidRecordCount":1`, 1),
		strings.Replace(valid, `"rdkitVersion":"2024.03.5"`, `"rdkitVersion":""`, 1),
	} {
		if _, err := decodeScientificArtifactValidation(strings.NewReader(malformed), "smi", "2024.03.5"); err == nil {
			t.Fatalf("contradictory locations accepted: %s", malformed)
		}
	}
	if _, err := decodeScientificArtifactValidation(strings.NewReader(strings.Replace(valid, `"format":"smi"`, `"format":"sdf"`, 1)), "sdf", "2024.03.5"); err == nil {
		t.Fatal("unavailable SDF physical line positions accepted")
	}
}
