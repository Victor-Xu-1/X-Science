package server

import (
	"encoding/json"
	"errors"

	"synon-go/internal/persistence/runtimekv"
)

const contextHistoryRetention = 128
const contextHistorySafeInteger = 9_007_199_254_740_991

// One sample per observed request. This is window history, not billing totals
// or conversation content. The top-level session identity preserves scoped
// task deletion through the existing runtimekv lifecycle contract.
type runnerContextHistory struct {
	SessionID     string               `json:"sessionId"`
	TotalObserved int                  `json:"totalObserved"`
	Coverage      string               `json:"coverage"`
	Samples       []runnerContextUsage `json:"samples"`
	Peak          *runnerContextUsage  `json:"peak,omitempty"`
}

func decodeRunnerContextHistory(entry runtimekv.Entry, sessionID string) (runnerContextHistory, error) {
	var wire struct {
		SessionID     string            `json:"sessionId"`
		TotalObserved int               `json:"totalObserved"`
		Coverage      string            `json:"coverage"`
		Samples       []json.RawMessage `json:"samples"`
		Peak          json.RawMessage   `json:"peak"`
	}
	raw, err := json.Marshal(entry.Value)
	if err == nil {
		err = json.Unmarshal(raw, &wire)
	}
	history := runnerContextHistory{SessionID: wire.SessionID, TotalObserved: wire.TotalObserved, Coverage: wire.Coverage}
	if err != nil || history.SessionID != sessionID || history.Coverage != "recorded" ||
		len(wire.Samples) == 0 || len(wire.Samples) > contextHistoryRetention ||
		history.TotalObserved < len(wire.Samples) || history.TotalObserved > contextHistorySafeInteger {
		return history, errors.New("invalid context history record")
	}
	seen := make(map[string]bool, len(wire.Samples))
	if len(wire.Peak) > 0 {
		peak, err := decodeRunnerContextUsage(runtimekv.Entry{Value: wire.Peak})
		if err != nil || peak.SessionID != sessionID || peak.Source != "provider" || peak.State != "complete" {
			return history, errors.New("invalid context history peak")
		}
		history.Peak = &peak
	}
	for _, sample := range wire.Samples {
		validated, err := decodeRunnerContextUsage(runtimekv.Entry{Value: sample})
		if err != nil || validated.SessionID != sessionID || seen[validated.RequestID] {
			return history, errors.New("invalid context history sample")
		}
		seen[validated.RequestID] = true
		if validated.Source == "provider" && (history.Peak == nil || validated.UsedTokens > history.Peak.UsedTokens) {
			return history, errors.New("context history peak omits a provider sample")
		}
		history.Samples = append(history.Samples, validated)
	}
	return history, nil
}

func recordRunnerContextHistory(entries map[string]runtimekv.Entry, snapshot runnerContextUsage) error {
	history := runnerContextHistory{SessionID: snapshot.SessionID, Coverage: "recorded"}
	if entry, found := entries["history"]; found {
		var err error
		history, err = decodeRunnerContextHistory(entry, snapshot.SessionID)
		if err != nil {
			return err
		}
	} else if entry, found := entries["latest"]; found {
		previous, err := decodeRunnerContextUsage(entry)
		if err == nil {
			history.Samples = append(history.Samples, previous)
			history.TotalObserved = 1
			if previous.Source == "provider" {
				history.Peak = &previous
			}
		} else if !errors.Is(err, errLegacyRunnerContextUsage) {
			return err
		}
	}
	if len(history.Samples) > 0 && history.Samples[len(history.Samples)-1].RequestID == snapshot.RequestID {
		history.Samples[len(history.Samples)-1] = snapshot
	} else {
		if history.TotalObserved == contextHistorySafeInteger {
			return errors.New("context history count exceeded safe integer")
		}
		history.Samples = append(history.Samples, snapshot)
		history.TotalObserved++
		if len(history.Samples) > contextHistoryRetention {
			history.Samples = history.Samples[len(history.Samples)-contextHistoryRetention:]
		}
	}
	if snapshot.Source == "provider" && (history.Peak == nil || snapshot.UsedTokens >= history.Peak.UsedTokens) {
		peak := snapshot
		history.Peak = &peak
	}
	entries["history"] = runtimekv.Entry{Value: history}
	return nil
}

// Latest and history are read under one store lock. A poll cannot mix the
// previous request's history with the next request's stream or model capacity.
func readRunnerContextUsage(store *runtimekv.Store, sessionID string) (runnerContextUsage, runnerContextHistory, bool, error) {
	entries, err := store.ListReadOnly(contextUsageNamespace(sessionID))
	if err != nil {
		return runnerContextUsage{}, runnerContextHistory{}, false, err
	}
	var latestEntry, historyEntry runtimekv.Entry
	for _, entry := range entries {
		switch entry.Key {
		case "latest":
			latestEntry = entry
		case "history":
			historyEntry = entry
		}
	}
	if latestEntry.Key == "" {
		return runnerContextUsage{}, runnerContextHistory{}, false, nil
	}
	snapshot, err := decodeRunnerContextUsage(latestEntry)
	if err != nil || snapshot.SessionID != sessionID {
		if err == nil {
			err = errors.New("context usage belongs to another session")
		}
		return snapshot, runnerContextHistory{}, true, err
	}
	history := runnerContextHistory{SessionID: sessionID, TotalObserved: 1, Coverage: "latest_only", Samples: []runnerContextUsage{snapshot}}
	if historyEntry.Key != "" {
		history, err = decodeRunnerContextHistory(historyEntry, sessionID)
		if err != nil {
			return snapshot, history, true, err
		}
		last := len(history.Samples) - 1
		if history.Samples[last].RequestID != snapshot.RequestID {
			return snapshot, history, true, errors.New("context history order does not match latest request")
		}
		// Streams update only latest; history projection follows that exact
		// sample without rewriting the bounded history on every text chunk.
		history.Samples[last] = snapshot
	}
	return snapshot, history, true, nil
}
