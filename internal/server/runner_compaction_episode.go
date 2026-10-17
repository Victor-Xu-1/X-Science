package server

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

const runnerSoftCompactionEpisodeField = "softCompactionEpisode"

// This projection contains public model and soft-budget policy values only.
// Execution identity, credentials and the original selection never enter it.
type runnerCompactionModel struct {
	selectionKind     string
	selectionRevision int64
	providerID        string
	modelID           string
	capacity          runnerContextCapacity
}

type runnerCompactionBinding struct {
	Version           int    `json:"version"`
	SelectionKind     string `json:"selectionKind"`
	SelectionRevision int64  `json:"selectionRevision"`
	ProviderIDKnown   bool   `json:"providerIdKnown"`
	ProviderID        string `json:"providerId"`
	ModelID           string `json:"modelId"`
	WindowTokens      int    `json:"windowTokens"`
	WindowSource      string `json:"windowSource"`
	ThresholdTokens   int    `json:"thresholdTokens"`
}

// Preparation owns the episode until it hands the same object to the request
// budget. All later transitions run under that budget's existing mutex.
type runnerCompactionEpisode struct {
	binding runnerCompactionBinding
	armed   bool
}

func runnerCompactionSelectionKind(selection string) string {
	selection = strings.TrimSpace(selection)
	switch {
	case selection == "":
		return "default"
	case strings.HasPrefix(selection, webConversationProviderSelectionPrefix):
		return "provider"
	default:
		return "model"
	}
}

func (model runnerCompactionModel) binding(threshold int) runnerCompactionBinding {
	providerID := strings.TrimSpace(model.providerID)
	return runnerCompactionBinding{
		Version: 1, SelectionKind: model.selectionKind, SelectionRevision: model.selectionRevision,
		ProviderIDKnown: providerID != "", ProviderID: providerID, ModelID: strings.TrimSpace(model.modelID),
		WindowTokens: model.capacity.Tokens, WindowSource: model.capacity.Source, ThresholdTokens: threshold,
	}
}

func (episode *runnerCompactionEpisode) bind(binding runnerCompactionBinding) {
	if episode.binding != binding {
		episode.binding = binding
		episode.armed = true
	}
}

func restoreRunnerCompactionEpisode(binding runnerCompactionBinding, entries []eventjournal.Entry) *runnerCompactionEpisode {
	episode := &runnerCompactionEpisode{binding: binding, armed: true}
	// A fresh compact never erases a newer, unresolved provider/request failure.
	if runnerContextPressureRequiringCompaction(entries) != "" {
		return episode
	}
	entry, fresh := latestFreshRunnerCompaction(entries)
	if fresh {
		if prior, bound := runnerCompactionBindingFromMessage(entry.Message); bound && prior == binding {
			episode.armed = false
		}
	}
	return episode
}

func latestFreshRunnerCompaction(entries []eventjournal.Entry) (eventjournal.Entry, bool) {
	boundary := -1
	for index := len(entries) - 1; index >= 0; index-- {
		if isCompactJournalEntry(entries[index]) {
			boundary = index
			break
		}
	}
	if boundary < 0 {
		return eventjournal.Entry{}, false
	}
	for _, entry := range entries[boundary+1:] {
		message := entry.Message
		switch strings.TrimSpace(stringValue(message["type"])) {
		case "message":
			role := strings.TrimSpace(stringValue(message["role"]))
			if role == "user" || role == "assistant" {
				return eventjournal.Entry{}, false
			}
		case "runner_checkpoint":
			if runnerCompactionNewToolOutcome(entries[boundary], entry) {
				return eventjournal.Entry{}, false
			}
		}
	}
	return entries[boundary], true
}

func runnerCompactionNewToolOutcome(boundary, candidate eventjournal.Entry) bool {
	if !runnerCompactionToolOutcome(candidate.Message) {
		return false
	}
	// Only the existing canonical item->terminal relation proves new outcome.
	// Call IDs and attempts can be reused; payload origin fields and compact
	// continuity cannot substitute for this read-side provenance. Republished
	// old or legacy outcomes stay unknown without blocking request execution.
	outcome, known := candidate.RuntimeProjection.(transcriptstore.RunnerReplayToolOutcome)
	return known && outcome.BatchID != "" && outcome.Ordinal >= 0 &&
		outcome.ToolCallID == stringValue(candidate.Message["toolCallId"]) &&
		outcome.SourceEventID > 0 && outcome.SourceEventID < outcome.TerminalEventID &&
		outcome.TerminalEventID == candidate.EventID && outcome.TerminalEventID > boundary.EventID
}

func runnerCompactionToolOutcome(message eventjournal.Message) bool {
	if stringValue(message["toolCallId"]) == "" || boolValue(message["rejectedBeforeExecution"], false) {
		return false
	}
	phase := stringValue(message["toolPhase"])
	if phase != "completed" && phase != "failed" {
		return false
	}
	result, _ := message["toolResult"].(map[string]any)
	return result["executed"] != false
}

func runnerReplayFreshlyCompacted(entries []eventjournal.Entry) bool {
	_, fresh := latestFreshRunnerCompaction(entries)
	return fresh
}

func (binding runnerCompactionBinding) checkpointValue() (runnerCompactionBinding, error) {
	// Public IDs have no separate length cap in the provider contract. Preserve
	// them in full and let the existing bounded checkpoint writer enforce size.
	// Reject invalid UTF-8 rather than letting JSON silently replace identity.
	if !utf8.ValidString(binding.ProviderID) || !utf8.ValidString(binding.ModelID) {
		return runnerCompactionBinding{}, errors.New("soft compaction public model identity is not valid UTF-8")
	}
	return binding, nil
}

func runnerCompactionBindingFromMessage(message eventjournal.Message) (runnerCompactionBinding, bool) {
	raw, present := message[runnerSoftCompactionEpisodeField]
	if !present {
		return runnerCompactionBinding{}, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return runnerCompactionBinding{}, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil || len(fields) != 9 {
		return runnerCompactionBinding{}, false
	}
	for _, key := range []string{"version", "selectionKind", "selectionRevision", "providerIdKnown", "providerId", "modelId", "windowTokens", "windowSource", "thresholdTokens"} {
		value, exists := fields[key]
		if !exists || strings.TrimSpace(string(value)) == "null" {
			return runnerCompactionBinding{}, false
		}
	}
	var binding runnerCompactionBinding
	if err := json.Unmarshal(encoded, &binding); err != nil || binding.Version != 1 ||
		binding.ProviderIDKnown != (binding.ProviderID != "") || binding.WindowTokens < 0 || binding.ThresholdTokens < 0 {
		return runnerCompactionBinding{}, false
	}
	switch binding.SelectionKind {
	case "default", "provider", "model":
	default:
		return runnerCompactionBinding{}, false
	}
	switch binding.WindowSource {
	case "unknown", "model_profile", "configured":
	default:
		return runnerCompactionBinding{}, false
	}
	return binding, true
}
