package sciencecapability

import (
	"errors"
	"strings"
)

// ExecutionInputEvidence binds a file parameter to a declared output of an
// allowed resolver. Only a successful host receipt can establish provenance.
type ExecutionInputEvidence struct {
	EvidenceGroup string `json:"evidenceGroup"`
	OutputKind    string `json:"outputKind"`
}

// ExecutionInputLineage compares the consumer's input bytes with a digest in
// the producer's receipt-bound JSON output. The pointer is registry-owned, not
// model supplied. No model-authored JSON can establish this relationship.
type ExecutionInputLineage struct {
	EvidenceGroup string `json:"evidenceGroup"`
	InputKind     string `json:"inputKind"`
	OutputKind    string `json:"outputKind"`
	SHA256Pointer string `json:"sha256Pointer"`
}

func executionInputEvidenceGroup(pack ExecutionPack, group string) bool {
	for _, parameter := range pack.Parameters {
		if parameter.InputEvidence != nil && parameter.InputEvidence.EvidenceGroup == group {
			return true
		}
	}
	return false
}

// EvidenceResolverPacks resolves the closed machine relationship, independent
// of task text, filenames, aliases or prose supplied by an external tool.
func (catalog Catalog) EvidenceResolverPacks(resolver ExecutionEvidenceResolver) []ExecutionPack {
	var packs []ExecutionPack
	for _, capability := range catalog.Capabilities {
		for _, engine := range capability.AcceptedEngines {
			pack := engine.ExecutionPack
			if pack.Mode == "local" && strings.EqualFold(pack.Skill, resolver.Skill) &&
				(strings.EqualFold(engine.ID, resolver.Implementation) ||
					engine.Package != "" && strings.EqualFold(engine.Package, resolver.Implementation)) {
				packs = append(packs, pack)
			}
		}
	}
	return packs
}

func (catalog Catalog) validateExecutionInputEvidence(pack ExecutionPack) error {
	if err := validateDocumentedInputs(pack); err != nil {
		return err
	}
	if len(pack.InputLineage) > 64 {
		return errors.New("too many input lineage bindings")
	}
	groups := map[string]map[string]bool{}
	for _, parameter := range pack.Parameters {
		binding := parameter.InputEvidence
		if binding == nil {
			continue
		}
		if pack.Mode != "local" || parameter.Type != "string" || parameter.Default != nil ||
			parameter.Evidence != "" || parameter.EvidenceGroup != "" || len(parameter.EvidenceTerms) != 0 ||
			!identifierPattern.MatchString(binding.EvidenceGroup) || !identifierPattern.MatchString(binding.OutputKind) {
			return errors.New("invalid execution-owned file parameter")
		}
		for _, input := range pack.Inputs {
			if parameter.Argument == input.Argument {
				return errors.New("input evidence argument overlaps a primary input")
			}
		}
		if groups[binding.EvidenceGroup] == nil {
			groups[binding.EvidenceGroup] = map[string]bool{}
		}
		if groups[binding.EvidenceGroup][binding.OutputKind] {
			return errors.New("duplicate input evidence output kind")
		}
		groups[binding.EvidenceGroup][binding.OutputKind] = true
	}
	lineageKeys := map[string]bool{}
	for _, lineage := range pack.InputLineage {
		key := lineage.EvidenceGroup + "\x00" + lineage.InputKind
		inputFound := false
		for _, input := range pack.Inputs {
			inputFound = inputFound || input.Kind == lineage.InputKind
		}
		if groups[lineage.EvidenceGroup] == nil || !inputFound || !identifierPattern.MatchString(lineage.OutputKind) ||
			!validDigestPointer(lineage.SHA256Pointer) || lineageKeys[key] {
			return errors.New("invalid or duplicate input lineage binding")
		}
		lineageKeys[key] = true
	}
	for group, kinds := range groups {
		found := false
		for _, resolver := range pack.EvidenceResolvers {
			if resolver.EvidenceGroup != group {
				continue
			}
			producers := catalog.EvidenceResolverPacks(resolver)
			if len(producers) != 1 {
				return errors.New("input evidence resolver must identify one local execution pack")
			}
			producer := producers[0]
			if producer.ID == pack.ID {
				return errors.New("input evidence cannot consume its own execution")
			}
			outputs := map[string]ExecutionOutput{}
			for _, output := range producer.Outputs {
				outputs[output.Kind] = output
			}
			for kind := range kinds {
				if _, ok := outputs[kind]; !ok {
					return errors.New("input evidence output kind is unavailable")
				}
			}
			for _, lineage := range pack.InputLineage {
				if lineage.EvidenceGroup == group && outputs[lineage.OutputKind].Format != "json" {
					return errors.New("input lineage requires a declared JSON output")
				}
			}
			found = true
		}
		if !found {
			return errors.New("input evidence group has no resolver")
		}
	}
	return nil
}

func validDigestPointer(pointer string) bool {
	if !validBoundedText(pointer, 512) || !strings.HasPrefix(pointer, "/") {
		return false
	}
	for i := 0; i < len(pointer); i++ {
		if pointer[i] == '~' {
			i++
			if i >= len(pointer) || pointer[i] != '0' && pointer[i] != '1' {
				return false
			}
		}
	}
	return true
}
