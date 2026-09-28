package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"synon-go/internal/sciencecapability"
)

// A documented assumption is deliberately not an execution receipt. Source
// references are retained claims to review, never proof of external truth or
// instructions to fetch a URL. The kernel checks exact bytes and parameters.
type executionDocumentedInput struct {
	Schema        string         `json:"schema"`
	EvidenceGroup string         `json:"evidence_group"`
	InputSHA256   string         `json:"input_sha256"`
	Basis         string         `json:"basis"`
	Method        string         `json:"method"`
	Sources       []string       `json:"sources"`
	Limitations   string         `json:"limitations"`
	Values        map[string]any `json:"values"`
}

func (s *Server) bindDocumentedExecutionInputs(ctx context.Context, root, workingDir string, pack sciencecapability.ExecutionPack, input map[string]any) (map[string]any, map[string]any, error) {
	if len(pack.DocumentedInputs) == 0 {
		return input, nil, nil
	}
	args, values, err := executionEvidenceArguments(pack, stringValue(input["command"]))
	if err != nil {
		return input, documentedInputBoundary(pack.ID, "", "invalid_arguments"), nil
	}
	replacements := map[string]string{}
	for _, route := range pack.DocumentedInputs {
		if values[route.Argument] == "" {
			continue
		}
		bound, err := s.bindDocumentedInput(ctx, root, workingDir, pack, route, values)
		if err != nil {
			if ctx.Err() != nil {
				return input, nil, ctx.Err()
			}
			reason := "invalid_documented_input"
			if errors.Is(err, errExecutionInputMaterialization) {
				reason = "materialization_unavailable"
			}
			return input, documentedInputBoundary(pack.ID, route.EvidenceGroup, reason), nil
		}
		for key, value := range bound {
			if previous, found := replacements[key]; found && previous != value {
				return input, documentedInputBoundary(pack.ID, route.EvidenceGroup, "conflicting_inputs"), nil
			}
			replacements[key] = value
		}
	}
	if len(replacements) == 0 {
		return input, nil, nil
	}
	for i := 2; i < len(args)-1; i++ {
		if value, ok := replacements[args[i]]; ok {
			args[i+1] = value
			i++
		}
	}
	for i := range args {
		args[i] = shellSingleQuote(args[i])
	}
	normalized := copyMapAny(input)
	normalized["command"] = strings.Join(args, " ")
	return normalized, nil, nil
}

func (s *Server) bindDocumentedInput(ctx context.Context, root, workingDir string, pack sciencecapability.ExecutionPack, route sciencecapability.ExecutionDocumentedInput, values map[string]string) (map[string]string, error) {
	file, _, err := openExecutionEvidenceFile(root, workingDir, values[route.Argument])
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > 1<<20 {
		return nil, errors.New("documented input exceeds size bound")
	}
	raw, err := io.ReadAll(io.LimitReader(&contextKernelReader{ctx: ctx, reader: file}, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 1<<20 {
		return nil, errors.New("documented input exceeds size bound")
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var record executionDocumentedInput
	if err = decoder.Decode(&record); err != nil {
		return nil, err
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, errors.New("trailing input evidence")
	}
	if err = validateDocumentedInputRecord(record, pack, route, values); err != nil {
		return nil, err
	}
	inputArgument := ""
	for _, binding := range pack.Inputs {
		if binding.Kind == route.InputKind {
			inputArgument = binding.Argument
			break
		}
	}
	if inputArgument == "" {
		return nil, errors.New("missing input binding")
	}
	primary, err := s.materializeExecutionInputFile(ctx, root, workingDir, values[inputArgument], record.InputSHA256)
	if err != nil {
		return nil, err
	}
	document, err := s.materializeExecutionInputFile(ctx, root, workingDir, values[route.Argument], digest)
	if err != nil {
		return nil, err
	}
	return map[string]string{inputArgument: primary, route.Argument: document}, nil
}

func validateDocumentedInputRecord(record executionDocumentedInput, pack sciencecapability.ExecutionPack, route sciencecapability.ExecutionDocumentedInput, values map[string]string) error {
	if record.Schema != "synon.documented-input.v1" || record.EvidenceGroup != route.EvidenceGroup ||
		!boundedDocumentedText(record.Method, 16384) || !boundedDocumentedText(record.Limitations, 16384) || len(record.Sources) == 0 || len(record.Sources) > 64 {
		return errors.New("incomplete documented input")
	}
	switch record.Basis {
	case "literature-guided", "structure-derived", "user-supplied", "exploratory":
	default:
		return errors.New("unknown documented input basis")
	}
	for _, source := range record.Sources {
		if !boundedDocumentedText(source, 4096) {
			return errors.New("invalid source reference")
		}
	}
	expected := 0
	for _, parameter := range pack.Parameters {
		if parameter.InputEvidence != nil && parameter.InputEvidence.EvidenceGroup == route.EvidenceGroup && values[parameter.Argument] != "" {
			return errors.New("documented input cannot claim automatic resolver output")
		}
		if parameter.EvidenceGroup != route.EvidenceGroup || parameter.Evidence != "resolved-user-input" {
			continue
		}
		expected++
		provided, found := record.Values[parameter.Name]
		if !found || values[parameter.Argument] == "" {
			return errors.New("incomplete documented parameter group")
		}
		switch parameter.Type {
		case "number", "integer":
			actual, err := strconv.ParseFloat(values[parameter.Argument], 64)
			number, ok := provided.(float64)
			if err != nil || !ok || math.IsNaN(actual) || math.IsInf(actual, 0) || actual != number ||
				parameter.Type == "integer" && math.Trunc(actual) != actual ||
				parameter.Minimum != nil && actual < *parameter.Minimum || parameter.Maximum != nil && actual > *parameter.Maximum {
				return errors.New("documented parameter value mismatch")
			}
		case "string":
			if value, ok := provided.(string); !ok || value != values[parameter.Argument] {
				return errors.New("documented parameter value mismatch")
			}
		default:
			return errors.New("unsupported documented parameter type")
		}
	}
	if expected == 0 || len(record.Values) != expected {
		return errors.New("unexpected documented parameters")
	}
	return nil
}

func boundedDocumentedText(value string, maximum int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= maximum && !strings.ContainsRune(value, 0)
}

func (s *Server) materializeExecutionInputFile(ctx context.Context, root, workingDir, value, expectedDigest string) (string, error) {
	file, source, err := openExecutionEvidenceFile(root, workingDir, value)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest, err := executionEvidenceFileDigest(ctx, file)
	if err != nil {
		return "", err
	}
	if digest != expectedDigest {
		return "", errExecutionInputLineageMismatch
	}
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	receiptRoot, err := s.kernelMaterializationReceiptRoot()
	if err != nil {
		return "", errors.Join(errExecutionInputMaterialization, err)
	}
	path, err := materializeKernelImmutableContent(ctx, root, receiptRoot, "input-"+digest, filepath.Base(source), info.Size(), digest, file)
	if err != nil {
		return "", errors.Join(errExecutionInputMaterialization, err)
	}
	return path, nil
}

func documentedInputBoundary(pack, group, reason string) map[string]any {
	result := map[string]any{
		"ok": false, "executed": false, "preflight": true, "status": "documented_input_required", "execution_pack_id": pack, "evidence_group": group, "reason": reason,
		"message":  "The documented alternative must match the current input and parameter values, with its own sources, method and limitations.",
		"recovery": "Use the declared documented-input argument with schema synon.documented-input.v1, evidence_group, input_sha256, basis (literature-guided, structure-derived, user-supplied or exploratory), method, sources, limitations and values keyed by parameter name. Do not attach prediction receipts to this independent route or claim automatic validation. Input provenance is not proof of scientific validity.",
	}
	if reason == "materialization_unavailable" {
		result["retryable"] = true
		result["recovery"] = "Restore immutable-file access and retry the documented handoff; do not rerun the upstream scientific work."
	}
	return result
}
