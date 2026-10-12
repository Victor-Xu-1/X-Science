package mcpstdio

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
	"x-science/internal/persistence/productstate"
	secretstore "x-science/internal/persistence/secrets"
)

func TestOAuthNamespaceUpgradePreservesActualCiphertextAndIsolation(t *testing.T) {
	root := t.TempDir()
	previous := filepath.Join(root, ".synon", "mcp-oauth-vault")
	store := secretstore.New(previous)
	identity := "connector-namespace-fixture"
	_, err := store.Create(secretstore.Secret{ID: oauthTokenSecretID(identity), UserID: oauthTokenVaultUserID, Provider: "mcp-oauth", Credentials: map[string]string{"accessToken": "fixture-bearer", "tokenType": "Bearer", "expiresAt": "0", "origin": "https://api.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create(secretstore.Secret{ID: oauthTokenSecretID("other-owner"), UserID: "another-user", Provider: "mcp-oauth", Credentials: map[string]string{"accessToken": "private-other-owner", "expiresAt": "0"}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(previous, "secrets/vault.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if err := UpgradePrivateNamespace(root); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, productstate.HiddenDirectory, "mcp-oauth-vault/secrets/vault.enc")
	after, err := os.ReadFile(current)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("namespace changed actual encrypted bytes")
	}
	if token, ok := loadOAuthAccessTokenForRemote(root, ServerConfig{ConnectorID: identity, URL: "https://api.example.test/mcp"}); !ok || token != "fixture-bearer" {
		t.Fatal("existing encrypted token cannot be resolved")
	}
	if _, ok := loadOAuthAccessToken(root, "other-owner"); ok {
		t.Fatal("foreign owner token was exposed")
	}
	if _, ok := loadOAuthAccessToken(root, "wrong-connector"); ok {
		t.Fatal("wrong connector token was exposed")
	}
	if err := saveOAuthToken(root, "new-connector", oauthTokenCache{AccessToken: "new-canonical-fixture", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := UpgradePrivateNamespace(root); err != nil {
		t.Fatal(err)
	}
	retained, _ := os.ReadFile(filepath.Join(previous, "secrets/vault.enc"))
	if !bytes.Equal(retained, before) {
		t.Fatal("new write modified previous recovery vault")
	}
	if token, ok := loadOAuthAccessToken(root, "new-connector"); !ok || token != "new-canonical-fixture" {
		t.Fatal("new canonical write was lost")
	}
}

func TestOAuthNamespaceCollisionDoesNotCreateEmptyVault(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, ".synon/mcp-oauth-vault/secrets")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "vault.enc"), []byte("retained-original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, productstate.HiddenDirectory)); err != nil {
		t.Fatal(err)
	}
	if err := saveOAuthToken(root, "fixture", oauthTokenCache{AccessToken: "must-not-write"}); err == nil {
		t.Fatal("foreign target accepted")
	}
	if raw, _ := os.ReadFile(filepath.Join(source, "vault.enc")); string(raw) != "retained-original" {
		t.Fatal("original credentials were changed")
	}
}
