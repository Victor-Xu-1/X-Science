package server

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"x-science/internal/skills"
)

type compatibilitySkillValidationError struct {
	code    string
	message string
	cause   error
}

func (err *compatibilitySkillValidationError) Error() string { return err.message }
func (err *compatibilitySkillValidationError) Unwrap() error { return err.cause }

func skillValidationError(code, message string, cause error) error {
	return &compatibilitySkillValidationError{code: code, message: message, cause: cause}
}

// Validate declarative package structure only. Python, shell and other helper
// resources are data here; validation must never load or execute their code.
func validateCompatibilitySkillPackage(root, name string) (skills.Skill, error) {
	if err := verifyWritableCompatibilitySkillRoot(root); err != nil {
		return skills.Skill{}, skillValidationError("unsafe_package_root", "Skill package root is not a verified regular directory", err)
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return skills.Skill{}, skillValidationError("unreadable_package_root", "Skill package root cannot be opened safely", err)
	}
	defer directory.Close()
	if err := validateCompatibilitySkillDraftResources(directory); err != nil {
		return skills.Skill{}, err
	}
	if err := validateCompatibilitySkillManifest(directory, name); err != nil {
		return skills.Skill{}, err
	}
	loaded := skills.LoadFromRoot(directory, root, runtimeSkillLoadOptions())
	if len(loaded.LoadErrors()) != 0 || len(loaded.Skills()) != 1 {
		failures := make([]error, 0, len(loaded.LoadErrors()))
		for _, failure := range loaded.LoadErrors() {
			failures = append(failures, errors.New(failure.Err))
		}
		return skills.Skill{}, skillValidationError("invalid_package_structure", "Skill package must contain exactly one valid root SKILL.md within the runtime contract", errors.Join(failures...))
	}
	skill := loaded.Skills()[0]
	if skill.Name != name || filepath.Clean(skill.Path) != filepath.Join(root, "SKILL.md") {
		return skills.Skill{}, skillValidationError("manifest_name_mismatch", "Skill manifest name must match its managed directory", nil)
	}
	return skill, nil
}

// The general catalog loader intentionally accepts legacy manifests. Explicit
// validation and publication require metadata, never its directory-name or
// missing-description fallbacks.
func validateCompatibilitySkillManifest(directory *os.Root, name string) error {
	source, err := openRegularWorkspaceRootFile(directory, "SKILL.md")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return skillValidationError("missing_skill_document", "Skill package requires SKILL.md at its root", err)
		}
		return skillValidationError("unsafe_skill_document", "Skill manifest cannot be read as a safe regular file", err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(source, maxSkillArchiveBytes+1))
	if err := errors.Join(readErr, source.Close()); err != nil {
		return skillValidationError("unreadable_skill_document", "Skill manifest could not be read safely", err)
	}
	if len(raw) > maxSkillArchiveBytes || !utf8.Valid(raw) || strings.IndexByte(string(raw), 0) >= 0 {
		return skillValidationError("invalid_manifest_text", "Skill manifest must be bounded UTF-8 text without NUL", nil)
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return skillValidationError("missing_frontmatter", "Skill manifest requires YAML frontmatter", nil)
	}
	closing := 0
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			closing = index
			break
		}
	}
	if closing == 0 {
		return skillValidationError("invalid_frontmatter", "Skill manifest frontmatter is not closed", nil)
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:closing], "\n")), &document); err != nil ||
		len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return skillValidationError("invalid_frontmatter", "Skill manifest frontmatter must be a valid YAML mapping", err)
	}
	metadata := document.Content[0]
	required := map[string]string{}
	for index := 0; index+1 < len(metadata.Content); index += 2 {
		key, value := metadata.Content[index], metadata.Content[index+1]
		if key.Value != "name" && key.Value != "description" {
			continue
		}
		if value.Kind != yaml.ScalarNode || value.Tag != "!!str" || strings.TrimSpace(value.Value) == "" {
			return skillValidationError("invalid_required_metadata", "Skill manifest name and description metadata must be non-empty strings", nil)
		}
		required[key.Value] = value.Value
	}
	if required["name"] == "" {
		return skillValidationError("missing_name", "Skill manifest is missing required name metadata", nil)
	}
	if required["name"] != name {
		return skillValidationError("manifest_name_mismatch", "Skill manifest name must match its managed directory", nil)
	}
	if strings.TrimSpace(required["description"]) == "" {
		return skillValidationError("missing_description", "Skill manifest is missing required description metadata", nil)
	}
	return nil
}

func (s *Server) kernelHostValidateSkill(name string) (any, error) {
	s.skillMutationMu.Lock()
	defer s.skillMutationMu.Unlock()
	skill, found, err := s.compatibilityAuthorSkill(name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, os.ErrNotExist
	}
	origin := s.compatibilitySkillSource(skill)
	if regularDraftFile(filepath.Join(filepath.Dir(skill.Path), compatibilitySkillDraftMarker)) {
		origin = "draft"
	}
	report := map[string]any{"valid": true, "name": skill.Name, "origin": origin,
		"errors": []map[string]string{}, "warnings": []map[string]string{}}
	if strings.HasPrefix(skill.Path, "builtin:") {
		return report, nil
	}
	var validationErr error
	if origin == "draft" {
		snapshot, err := captureSkillDraftSnapshot(skill)
		validationErr = err
		if err == nil {
			report["draft_revision"] = snapshot.revision
			validationErr = s.validateSkillDraftSnapshot(snapshot)
		}
	} else {
		_, validationErr = validateCompatibilitySkillPackage(filepath.Dir(skill.Path), skill.Name)
	}
	if err := validationErr; err != nil {
		report["valid"] = false
		code, message := "validation_unavailable", "Skill package validation could not complete safely"
		var diagnostic *compatibilitySkillValidationError
		if errors.As(err, &diagnostic) {
			code, message = diagnostic.code, diagnostic.message
		}
		report["errors"] = []map[string]string{{"code": code, "message": message}}
	}
	return report, nil
}
