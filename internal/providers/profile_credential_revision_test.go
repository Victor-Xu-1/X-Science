package providers

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	secretstore "x-science/internal/persistence/secrets"
	workspace "x-science/internal/persistence/workspace"
)

func TestModelProfileCredentialRevisionTracksSameReferenceRotation(t *testing.T) {
	for _, storage := range []string{"value", "credentials"} {
		t.Run(storage, func(t *testing.T) {
			root := t.TempDir()
			store := secretstore.New(root)
			secret := secretstore.Secret{ID: "provider-key", UserID: "owner", Provider: "openai"}
			const firstKey, secondKey = "fixture-old-api-value", "fixture-rotated-api-value"
			if storage == "value" {
				secret.Value = firstKey
			} else {
				secret.Credentials = map[string]string{"apiKey": firstKey}
			}
			created, err := store.Create(secret)
			if err != nil {
				t.Fatal(err)
			}
			provider := credentialRevisionTestProvider("secret://provider-key")
			before, err := BuildModelProfile(provider, store, "owner", ResolutionInput{})
			if err != nil || before.APIKey != firstKey || before.CredentialRevision != created.UpdatedAt.UTC().Format(time.RFC3339Nano) {
				t.Fatalf("initial owner-scoped profile/revision mismatch: %v", err)
			}
			assertProfileCredentialFieldsPrivate(t, before, firstKey)
			updated, err := store.UpdateForUser(secret.ID, "owner", func(value *secretstore.Secret) error {
				if storage == "value" {
					value.Value = secondKey
				} else {
					value.Credentials["apiKey"] = secondKey
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			// Reopen the real encrypted fixture store; the same SecretRef must
			// identify the newly stored revision and key in one authorized read.
			after, err := BuildModelProfile(provider, secretstore.New(root), "owner", ResolutionInput{})
			if err != nil || after.APIKey != secondKey || after.Provider.SecretRef != before.Provider.SecretRef ||
				after.CredentialRevision == before.CredentialRevision ||
				after.CredentialRevision != updated.UpdatedAt.UTC().Format(time.RFC3339Nano) {
				t.Fatalf("same-reference credential rotation was not reflected: %v", err)
			}
			assertProfileCredentialFieldsPrivate(t, after, firstKey, secondKey)
		})
	}
}

func TestModelProfileCredentialRevisionPreservesOwnerBoundary(t *testing.T) {
	store := secretstore.New(t.TempDir())
	if _, err := store.Create(secretstore.Secret{ID: "provider-key", UserID: "owner", Provider: "openai", Value: "fixture-owner-only"}); err != nil {
		t.Fatal(err)
	}
	profile, err := BuildModelProfile(credentialRevisionTestProvider("secret://provider-key"), store, "other-owner", ResolutionInput{})
	if err == nil || !strings.Contains(err.Error(), "not found") || profile.APIKey != "" || profile.CredentialRevision != "" {
		t.Fatal("cross-owner lookup exposed credential or revision")
	}
}

func TestModelProfileWithoutSecretHasNoCredentialRevision(t *testing.T) {
	profile, err := BuildModelProfile(credentialRevisionTestProvider(""), nil, "owner", ResolutionInput{})
	if err != nil || profile.APIKey != "" || profile.CredentialRevision != "" {
		t.Fatalf("credential-free profile requires a secret or invents a revision: %v", err)
	}
}

func credentialRevisionTestProvider(secretRef string) workspace.ModelProvider {
	return workspace.ModelProvider{ID: "fixture-provider", UserID: "owner", Name: "Fixture", Type: "openai",
		BaseURL: "https://provider.example/v1", Model: "fixture-model", SecretRef: secretRef, Enabled: true}
}

func assertProfileCredentialFieldsPrivate(t *testing.T, profile ModelProfile, keys ...string) {
	t.Helper()
	raw, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"APIKey", "apiKey", "CredentialRevision", "credentialRevision"} {
		if _, found := fields[name]; found {
			t.Fatalf("internal field %s leaked into profile JSON", name)
		}
	}
	for _, value := range append(keys, profile.CredentialRevision) {
		if value != "" && bytes.Contains(raw, []byte(value)) {
			t.Fatal("profile JSON exposed a credential value or its internal revision")
		}
	}
}
