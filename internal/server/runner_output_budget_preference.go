package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/persistence/runtimekv"
	"x-science/internal/providers"
)

// This is only a learned preference for omitting a disproven soft initial
// allowance. It contains no request context, usage, bound, endpoint or secret.
type outputBudgetPreference struct {
	Version               int    `json:"version"`
	Owner                 string `json:"ownerUserId"`
	Scope                 string `json:"scope"`
	PreferProviderDefault bool   `json:"preferProviderDefault"`
}

func outputBudgetPreferenceNamespace(profile providers.ModelProfile, role string) string {
	if profile.MaxTokens == nil || *profile.MaxTokens <= 0 || profile.Provider.UserID == "" ||
		profile.Provider.ID == "" || profile.Provider.Endpoint == "" || profile.Provider.Protocol == "" || profile.Model == "" || role == "" {
		return ""
	}
	// Opaque reference alone cannot distinguish in-place key rotation. Without
	// a store-supplied revision, retain task-local negotiation instead of sharing
	// an unproven account preference. Never derive or store a hash of the key.
	if (profile.Provider.SecretRef != "" || profile.APIKey != "") && profile.CredentialRevision == "" {
		return ""
	}
	// Exact configuration identity: a changed owner/provider/protocol/model,
	// role, allowance or temperature cannot inherit another profile's hint.
	identity, _ := json.Marshal([]any{profile.Provider.UserID, profile.Provider.ID,
		profile.Provider.Endpoint, profile.Provider.Protocol, profile.Provider.Type, profile.Provider.SecretRef, profile.CredentialRevision,
		profile.Model, role, *profile.MaxTokens, profile.Temperature})
	digest := sha256.Sum256(identity)
	return "output-default-preference-" + hex.EncodeToString(digest[:])
}

func (client *sessionOutputBudgetClient) decodeOutputBudgetPreference(entry runtimekv.Entry) (outputBudgetPreference, error) {
	var preference outputBudgetPreference
	raw, err := json.Marshal(entry.Value)
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&preference)
	}
	if err != nil || preference.Version != 1 || preference.Owner != client.owner ||
		preference.Scope != client.preferenceNamespace || !preference.PreferProviderDefault {
		return outputBudgetPreference{}, errors.New("output budget preference authority is invalid")
	}
	return preference, nil
}

// Freeze the first automatic choice once per task, so another task's later
// learning cannot replace in-progress automatic negotiation. Explicit caller
// budgets do not read or initialize this optional preference. Only a boolean is
// copied; Context, Next, Lower, Rejected and Ceiling remain task-local.
func (client *sessionOutputBudgetClient) initializeOutputBudgetPreference() (outputBudgetState, error) {
	var state outputBudgetState
	if client.preferenceNamespace == "" {
		return state, nil
	}
	entry, found, err := client.store.Get(client.preferenceNamespace, "preference")
	if err != nil {
		return state, err
	}
	if found {
		if _, err := client.decodeOutputBudgetPreference(entry); err != nil {
			return state, err
		}
	}
	err = client.store.EditNamespace(client.namespace, func(entries map[string]runtimekv.Entry) (bool, error) {
		if current, exists := entries["state"]; exists {
			var err error
			state, err = client.decode(current)
			return false, err
		}
		state = outputBudgetState{Owner: client.owner, Session: client.session, SkipInitialAllowance: found}
		entries["state"] = runtimekv.Entry{Value: state, Version: 1, UpdatedAt: time.Now().UTC()}
		return true, nil
	})
	return state, err
}

// Learn only from the complete, uncancelled success of a consumed comparison
// whose preceding silent length failure actually used this saved allowance.
// The preference is optional: failure to persist it never replays accepted work.
func (client *sessionOutputBudgetClient) rememberOutputBudgetPreference(ctx context.Context, probe outputBudgetState, response agentruntime.ModelResponse) error {
	if client.preferenceNamespace == "" || client.initialLimit <= 0 || ctx.Err() != nil ||
		!probe.DefaultProbeFromInitial || !probe.DefaultProbeUsed || probe.DefaultProbePending ||
		probe.Ceiling > 0 || !completeDefaultProbeResponse(response) {
		return nil
	}
	return client.store.EditNamespace(client.preferenceNamespace, func(entries map[string]runtimekv.Entry) (bool, error) {
		if ctx.Err() != nil {
			return false, nil
		}
		if existing, found := entries["preference"]; found {
			_, err := client.decodeOutputBudgetPreference(existing)
			return false, err
		}
		preference := outputBudgetPreference{Version: 1, Owner: client.owner, Scope: client.preferenceNamespace, PreferProviderDefault: true}
		entries["preference"] = runtimekv.Entry{Value: preference, Version: 1, UpdatedAt: time.Now().UTC()}
		return true, nil
	})
}
