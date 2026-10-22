package agentruntime

import (
	"encoding/json"
	"maps"
	"slices"
)

// SnapshotModelRequest freezes the typed request without a JSON round trip,
// which would lose host-only fields. It grants no execution authority.
func SnapshotModelRequest(request ModelRequest) ModelRequest {
	copy := request
	copy.Messages = slices.Clone(request.Messages)
	for index, message := range request.Messages {
		message.Parts = snapshotContentParts(message.Parts)
		message.pending = snapshotContentParts(message.pending)
		message.ToolCalls = slices.Clone(message.ToolCalls)
		for callIndex := range message.ToolCalls {
			message.ToolCalls[callIndex].Arguments = slices.Clone(message.ToolCalls[callIndex].Arguments)
			message.ToolCalls[callIndex].PreExecutionRejection = slices.Clone(message.ToolCalls[callIndex].PreExecutionRejection)
		}
		if message.sourceObservation != nil {
			observation := *message.sourceObservation
			message.sourceObservation = &observation
		}
		copy.Messages[index] = message
	}
	copy.Tools = slices.Clone(request.Tools)
	for index := range copy.Tools {
		copy.Tools[index].Parameters, _ = snapshotRequestJSON(request.Tools[index].Parameters).(map[string]any)
		copy.Tools[index].OutputSchema, _ = snapshotRequestJSON(request.Tools[index].OutputSchema).(map[string]any)
		copy.Tools[index].Capabilities = slices.Clone(request.Tools[index].Capabilities)
	}
	copy.Metadata, _ = snapshotRequestJSON(request.Metadata).(map[string]any)
	copy.Headers = maps.Clone(request.Headers)
	copy.MediaPolicy.AllowedFileRoots = slices.Clone(request.MediaPolicy.AllowedFileRoots)
	copy.ToolChoice = snapshotRequestJSON(request.ToolChoice)
	if request.Temperature != nil {
		temperature := *request.Temperature
		copy.Temperature = &temperature
	}
	return copy
}

func snapshotContentParts(parts []ContentPart) []ContentPart {
	copy := slices.Clone(parts)
	for index := range copy {
		if parts[index].Media != nil {
			media := *parts[index].Media
			media.Source.Data = slices.Clone(media.Source.Data)
			copy[index].Media = &media
		}
	}
	return copy
}

// Match the existing host JSON-value clone semantics, plus the typed JSON
// containers carried by ModelRequest. Do not reflect over arbitrary objects.
func snapshotRequestJSON(value any) any {
	switch value := value.(type) {
	case map[string]any:
		copy := maps.Clone(value)
		for key, child := range value {
			copy[key] = snapshotRequestJSON(child)
		}
		return copy
	case []any:
		copy := slices.Clone(value)
		for index, child := range value {
			copy[index] = snapshotRequestJSON(child)
		}
		return copy
	case []map[string]any:
		copy := slices.Clone(value)
		for index, child := range value {
			copy[index], _ = snapshotRequestJSON(child).(map[string]any)
		}
		return copy
	case map[string]string:
		return maps.Clone(value)
	case []string:
		return slices.Clone(value)
	case json.RawMessage:
		return slices.Clone(value)
	case []byte:
		return slices.Clone(value)
	default:
		return value
	}
}
