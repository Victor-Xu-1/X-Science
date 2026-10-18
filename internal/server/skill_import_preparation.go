package server

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"x-science/internal/skills"
)

// preparedSkillImport owns an unpublished staging directory. Its bytes,
// manifest and hash are finalized before taking the publication lock.
type preparedSkillImport struct {
	staging          string
	stagingIdentity  os.FileInfo
	retain           bool
	sourceRoot       string
	relativeManifest string
	skill            skills.Skill
	replace          bool
	files            []string
	description      *string
	hash             string
}

func (prepared *preparedSkillImport) cleanup() {
	if prepared.retain {
		return
	}
	info, err := os.Lstat(prepared.staging)
	if err != nil || prepared.stagingIdentity == nil || !os.SameFile(info, prepared.stagingIdentity) {
		return
	}
	_ = os.RemoveAll(prepared.staging)
}

func (s *Server) prepareSkillBundle(filename, requestedName string, replace bool, raw []byte) (_ *preparedSkillImport, err error) {
	extension := strings.ToLower(filepath.Ext(strings.TrimSpace(filename)))
	if extension != ".zip" && extension != ".skill" && extension != ".md" && extension != ".markdown" {
		return nil, errors.New("skill bundle must use .zip, .skill, .md, or .markdown")
	}
	installRoot := filepath.Join(s.fileRoot, "skills")
	if err := ensurePrivateDirectoryForImport(installRoot); err != nil {
		return nil, fmt.Errorf("prepare skill directory: %w", err)
	}
	staging, stagingIdentity, err := createPrivateSkillImportDirectory(installRoot)
	if err != nil {
		return nil, fmt.Errorf("create skill staging directory: %w", err)
	}
	prepared := &preparedSkillImport{staging: staging, stagingIdentity: stagingIdentity, replace: replace}
	defer func() {
		if err != nil {
			prepared.cleanup()
		}
	}()
	contentRoot := filepath.Join(staging, "package")
	if err := os.Mkdir(contentRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create staged skill package: %w", err)
	}
	isArchive := extension == ".zip" || (extension == ".skill" && len(raw) >= 4 && bytes.Equal(raw[:2], []byte("PK")))
	if isArchive {
		if err := extractSkillArchive(contentRoot, raw); err != nil {
			return nil, err
		}
	} else {
		if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
			return nil, errors.New("markdown skill is not valid UTF-8 text")
		}
		if err := os.WriteFile(filepath.Join(contentRoot, "SKILL.md"), raw, 0o600); err != nil {
			return nil, fmt.Errorf("stage markdown skill: %w", err)
		}
	}
	loaded := skills.Load([]string{contentRoot}, runtimeSkillLoadOptions())
	if loadErrors := loaded.LoadErrors(); len(loadErrors) > 0 {
		return nil, fmt.Errorf("invalid skill bundle: %s", loadErrors[0].Err)
	}
	items := loaded.Skills()
	if len(items) != 1 {
		return nil, fmt.Errorf("skill bundle must contain exactly one SKILL.md, found %d", len(items))
	}
	skill := items[0]
	manifest, err := os.ReadFile(skill.Path)
	if err != nil {
		return nil, fmt.Errorf("read staged SKILL.md: %w", err)
	}
	fallbackName := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	if requestedName != "" {
		fallbackName = requestedName
	}
	if !skillManifestHasName(manifest) {
		skill.Name = fallbackName
	}
	skill.Name = strings.TrimSpace(skill.Name)
	if !importedSkillNamePattern.MatchString(skill.Name) || skill.Name == "." || skill.Name == ".." {
		return nil, fmt.Errorf("invalid imported skill name %q", skill.Name)
	}
	prepared.skill = skill
	prepared.sourceRoot = filepath.Dir(skill.Path)
	prepared.relativeManifest, err = filepath.Rel(prepared.sourceRoot, skill.Path)
	if err != nil || prepared.relativeManifest == "." || !filepath.IsLocal(prepared.relativeManifest) {
		return nil, errors.New("staged skill manifest escaped its source directory")
	}
	prepared.files, err = listImportedSkillFiles(prepared.sourceRoot)
	if err != nil {
		return nil, err
	}
	if err := validateSkillImportResourceNames(prepared.files); err != nil {
		return nil, err
	}
	if skillManifestHasDescription(manifest) {
		prepared.description = &prepared.skill.Description
	}
	prepared.hash, err = webSkillDirectoryHash(prepared.sourceRoot)
	if err != nil {
		return nil, fmt.Errorf("verify staged skill: %w", err)
	}
	return prepared, nil
}

func (s *Server) importSkillBundle(filename, requestedName string, replace bool, raw []byte) (skillImportResult, error) {
	return s.importSkillBundleWithRecord(filename, requestedName, replace, raw, nil)
}

func (s *Server) importSkillBundleWithRecord(filename, requestedName string, replace bool, raw []byte, record func(skillImportResult) error) (skillImportResult, error) {
	prepared, err := s.prepareSkillBundle(filename, requestedName, replace, raw)
	if err != nil {
		return skillImportResult{}, err
	}
	defer prepared.cleanup()
	results, failed, err := s.publishPreparedSkillImports([]*preparedSkillImport{prepared}, immediateSkillDraftCommit, func(results []skillImportResult, _ []skillImportFailure) error {
		if record != nil && len(results) == 1 {
			return record(results[0])
		}
		return nil
	})
	if err != nil {
		return skillImportResult{}, err
	}
	if len(failed) != 0 {
		return skillImportResult{}, failed[0].err
	}
	return results[0], nil
}
