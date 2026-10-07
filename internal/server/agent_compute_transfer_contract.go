package server

import (
	"errors"
	"fmt"
	"strings"

	"synon-go/internal/compute/transfer"
)

func agentComputeOutputPolicy(outputs []any) (transfer.Policy, error) {
	// This bounds the control request, not the number/size of data files that
	// one recursive glob can select. Input/archive inventory is streamed.
	if len(outputs) > 256 {
		return transfer.Policy{}, errors.New("output selection exceeds 256 glob rules")
	}
	policy := transfer.Policy{}
	for _, raw := range outputs {
		output := transfer.Output{Glob: strings.TrimSpace(stringValue(raw)), Visibility: "featured"}
		if item, ok := raw.(map[string]any); ok {
			for key := range item {
				if key != "glob" && key != "visibility" {
					return transfer.Policy{}, fmt.Errorf("output field %q is not allowed", key)
				}
			}
			output.Glob = strings.TrimSpace(stringValue(item["glob"]))
			output.Visibility = strings.TrimSpace(firstNonEmpty(stringValue(item["visibility"]), "featured"))
		}
		policy.Outputs = append(policy.Outputs, output)
	}
	return policy, policy.Validate()
}

func agentComputeHarvestContract(input map[string]any) ([]string, map[string]any, error) {
	policy, err := agentComputeOutputPolicy(anySliceValue(input["outputs"]))
	if err != nil {
		return nil, nil, err
	}
	exclude := stringArrayValue(input["exclude"])
	if len(exclude) != len(anySliceValue(input["exclude"])) || len(exclude) > 256 {
		return nil, nil, errors.New("output exclusion requires at most 256 glob rules")
	}
	policy.Exclude = exclude
	policy.MaxFileBytes, policy.MaxTotalBytes, err = transfer.MegabyteBudgets(mapValue(input["transfer_limits"]))
	if err != nil {
		return nil, nil, err
	}
	if err := policy.Validate(); err != nil {
		return nil, nil, err
	}
	return exclude, map[string]any{"max_file_bytes": policy.MaxFileBytes, "max_total_bytes": policy.MaxTotalBytes}, nil
}
