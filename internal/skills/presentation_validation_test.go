package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePresentationSidecarWithoutPublishableManifest(t *testing.T) {
	for _, test := range []struct {
		name    string
		content *string
		valid   bool
	}{
		{name: "absent", valid: true},
		{name: "valid", content: presentationValidationText(`{"schema_version":1,"skills":{"fixture":{"category":"workflow","description_i18n":{"en":"Fixture"}}}}`), valid: true},
		{name: "invalid_json", content: presentationValidationText(`{"schema_version":`)},
		{name: "duplicate_key", content: presentationValidationText(`{"schema_version":1,"schema_version":1,"skills":{}}`)},
		{name: "unknown_field", content: presentationValidationText(`{"schema_version":1,"private_extra":true,"skills":{"fixture":{"category":"workflow","description_i18n":{"en":"Fixture"}}}}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\nname: [incomplete\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.content != nil {
				if err := os.WriteFile(filepath.Join(directory, presentationFilename), []byte(*test.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			root, err := os.OpenRoot(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if err := ValidatePresentationSidecar(root, presentationFilename); (err == nil) != test.valid {
				t.Fatalf("sidecar validity=%t err=%v", test.valid, err)
			}
		})
	}
}

func presentationValidationText(value string) *string { return &value }

func TestValidatePresentationSidecarRetainsRootAndFileSafety(t *testing.T) {
	inside, outside := t.TempDir(), t.TempDir()
	outsidePath := filepath.Join(outside, presentationFilename)
	if err := os.WriteFile(outsidePath, []byte(`{"schema_version":1,"skills":{"fixture":{"category":"workflow","description_i18n":{"en":"Fixture"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(inside, presentationFilename)); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(inside)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := ValidatePresentationSidecar(root, presentationFilename); err == nil {
		t.Fatal("sidecar symbolic link was accepted")
	}
	if err := ValidatePresentationSidecar(root, filepath.Join("..", presentationFilename)); err == nil {
		t.Fatal("sidecar path escaped its root")
	}
	if err := ValidatePresentationSidecar(nil, presentationFilename); err == nil {
		t.Fatal("nil sidecar root was accepted")
	}
	if err := ValidatePresentationSidecar(root, "ordinary-data.json"); err != nil {
		t.Fatalf("ordinary opaque resource acquired a sidecar schema: %v", err)
	}
}
