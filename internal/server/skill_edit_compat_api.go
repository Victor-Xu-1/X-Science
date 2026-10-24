package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"x-science/internal/skills"
)

var errCompatibilitySkillReadOnly = errors.New("skill is read-only")

func (s *Server) handleCompatibilitySkillMutation(w http.ResponseWriter, r *http.Request) {
	if !s.requireHostOperator(w, r) {
		return
	}
	segments := workspacePathSegments(strings.TrimPrefix(r.URL.Path, "/api/skills/"))
	if len(segments) < 1 || len(segments) > 2 {
		writeV11Detail(w, http.StatusNotFound, "Skill endpoint not found")
		return
	}
	if s.skillCatalog == nil || strings.TrimSpace(s.fileRoot) == "" {
		writeV11Detail(w, http.StatusServiceUnavailable, "Writable skill catalog is not configured")
		return
	}
	name, err := url.PathUnescape(strings.TrimSpace(segments[0]))
	if err != nil {
		writeV11Detail(w, http.StatusBadRequest, "Invalid skill name")
		return
	}
	if !importedSkillNamePattern.MatchString(name) || name == "." || name == ".." {
		writeV11Detail(w, http.StatusBadRequest, fmt.Sprintf("invalid skill name '%s'", name))
		return
	}
	if len(segments) == 1 {
		if r.Method != http.MethodDelete {
			writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		s.handleCompatibilitySkillDelete(w, r, name, true)
		return
	}
	switch segments[1] {
	case "edit":
		if r.Method != http.MethodPost {
			writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		s.handleCompatibilitySkillEdit(w, r, name)
	case "duplicate":
		if r.Method != http.MethodPost {
			writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		s.handleCompatibilitySkillDuplicate(w, r, name)
	case "publish":
		if r.Method != http.MethodPost {
			writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		s.handleCompatibilitySkillPublish(w, r, name)
	case "full":
		if r.Method != http.MethodDelete {
			writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		s.handleCompatibilitySkillDelete(w, r, name, false)
	default:
		writeV11Detail(w, http.StatusNotFound, "Skill endpoint not found")
	}
}

func (s *Server) handleCompatibilitySkillEdit(w http.ResponseWriter, r *http.Request, name string) {
	input := map[string]any{}
	if err := decodeAgentCompatJSON(r, &input); err != nil {
		writeV11Detail(w, http.StatusBadRequest, "Invalid skill edit request: "+err.Error())
		return
	}
	relativePath := stringValue(input["path"])
	oldString := stringValue(input["old_string"])
	newString, hasNewString := input["new_string"].(string)
	if strings.TrimSpace(relativePath) == "" || !hasNewString {
		writeV11Detail(w, http.StatusBadRequest, "path and new_string are required")
		return
	}
	if !utf8.ValidString(newString) || strings.IndexByte(newString, 0) >= 0 {
		writeV11Detail(w, http.StatusBadRequest, "new_string must be valid text without NUL bytes")
		return
	}
	s.skillMutationMu.Lock()
	defer s.skillMutationMu.Unlock()
	action, absolutePath, err := s.editCompatibilitySkillFile(name, relativePath, oldString, newString)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errCompatibilitySkillReadOnly) {
			status = http.StatusForbidden
		}
		writeV11Detail(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"action": action, "absolute_path": absolutePath})
}

func (s *Server) handleCompatibilitySkillDuplicate(w http.ResponseWriter, r *http.Request, name string) {
	var input struct {
		SourceName string `json:"sourceName"`
	}
	if err := decodeAgentCompatJSON(r, &input); err != nil {
		writeV11Detail(w, http.StatusBadRequest, "Invalid skill duplicate request: "+err.Error())
		return
	}
	input.SourceName = strings.TrimSpace(input.SourceName)
	if input.SourceName == "" {
		writeV11Detail(w, http.StatusBadRequest, "sourceName is required")
		return
	}
	s.skillMutationMu.Lock()
	defer s.skillMutationMu.Unlock()
	skill, err := s.duplicateCompatibilitySkill(input.SourceName, name)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, os.ErrExist) {
			status = http.StatusConflict
		}
		writeV11Detail(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"name": skill.Name, "draft": true})
}

func (s *Server) handleCompatibilitySkillPublish(w http.ResponseWriter, r *http.Request, name string) {
	var input struct {
		Overwrite             bool   `json:"overwrite"`
		ExpectedDraftRevision string `json:"expected_draft_revision"`
	}
	if err := decodeAgentCompatJSON(r, &input); err != nil {
		writeV11Detail(w, http.StatusBadRequest, "Invalid skill publish request: "+err.Error())
		return
	}
	s.skillMutationMu.Lock()
	defer s.skillMutationMu.Unlock()
	skill, err := s.publishCompatibilitySkillDraft(name, input.Overwrite, input.ExpectedDraftRevision)
	if err != nil {
		if failure, ok := skillDraftRevisionError(err); ok {
			status := http.StatusConflict
			if failure.Code == "draft_revision_required" {
				status = http.StatusPreconditionRequired
			}
			writeJSON(w, status, map[string]any{"code": failure.Code, "detail": failure.Message})
			return
		}
		writeV11Detail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": skill.Name, "published": true})
}

func (s *Server) handleCompatibilitySkillDelete(w http.ResponseWriter, r *http.Request, name string, draftOnly bool) {
	s.skillMutationMu.Lock()
	defer s.skillMutationMu.Unlock()
	if err := s.deleteCompatibilitySkill(compatAgentUserID(r), name, draftOnly); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		if errors.Is(err, errCompatibilitySkillReadOnly) {
			status = http.StatusForbidden
		}
		if errors.Is(err, errCompatibilitySkillAttached) {
			status = http.StatusConflict
		}
		writeV11Detail(w, status, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var errCompatibilitySkillAttached = errors.New("skill is attached to an agent")

func (s *Server) duplicateCompatibilitySkill(sourceName, targetName string) (skills.Skill, error) {
	if _, found := findCatalogSkill(s.skillCatalog, targetName); found {
		return skills.Skill{}, fmt.Errorf("%w: skill '%s' already exists", os.ErrExist, targetName)
	}
	source, found := findCatalogSkill(s.skillCatalog, sourceName)
	if !found {
		return skills.Skill{}, fmt.Errorf("source skill '%s' not found", sourceName)
	}
	if !strings.HasPrefix(source.Path, "builtin:") {
		return s.createCompatibilitySkillDraft(targetName, "SKILL.md", "", &source, immediateSkillDraftCommit)
	}
	manifest, err := compatibilitySkillManifest(source, targetName)
	if err != nil {
		return skills.Skill{}, err
	}
	return s.createCompatibilitySkillDraft(targetName, "SKILL.md", string(manifest), nil, immediateSkillDraftCommit)
}

func (s *Server) deleteCompatibilitySkill(userID, name string, draftOnly bool) error {
	return s.deleteCompatibilitySkillWithCommit(userID, name, draftOnly, immediateSkillDraftCommit)
}

func (s *Server) deleteCompatibilitySkillWithCommit(userID, name string, draftOnly bool, commit skillDraftCommit) error {
	skill, found := findCatalogSkill(s.skillCatalog, name)
	if draftOnly {
		var err error
		skill, found, err = s.findCompatibilitySkillDraft(name)
		if err != nil {
			return err
		}
	}
	if !found {
		return fmt.Errorf("%w: skill '%s' not found", os.ErrNotExist, name)
	}
	root, err := s.compatibilityEditableSkillRoot(skill)
	if err != nil {
		return err
	}
	if draftOnly && !regularDraftFile(filepath.Join(root, compatibilitySkillDraftMarker)) {
		return fmt.Errorf("skill '%s' is not a draft", name)
	}
	if !draftOnly && s.workspaceStore != nil {
		agents, err := s.workspaceStore.ListAgents(userID)
		if err != nil {
			return err
		}
		attached := make([]string, 0)
		for _, agent := range agents {
			for _, skillName := range agent.SkillNames {
				if strings.EqualFold(skillName, name) {
					attached = append(attached, agent.Name)
					break
				}
			}
		}
		if len(attached) > 0 {
			sort.Strings(attached)
			return fmt.Errorf("%w: skill '%s' is used by agents: %s", errCompatibilitySkillAttached, name, strings.Join(attached, ", "))
		}
	}
	trash := filepath.Join(filepath.Dir(root), ".skill-delete-"+uuid.NewString())
	if err := commit(func() error {
		if err := os.Rename(root, trash); err != nil {
			return err
		}
		if err := os.RemoveAll(trash); err != nil {
			return errors.Join(err, os.Rename(trash, root))
		}
		return nil
	}); err != nil {
		return err
	}
	if published, found := findCatalogSkill(s.skillCatalog, name); found && sameCleanPath(filepath.Dir(published.Path), root) {
		s.skillCatalog.RemoveSkill(name)
	}
	return nil
}

func verifyWritableCompatibilitySkillRoot(root string) error {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("writable skill root is not a regular directory")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || resolved != absolute {
		return errors.New("writable skill root is not a verified regular directory")
	}
	return nil
}

func compatibilitySkillManifest(skill skills.Skill, name string) ([]byte, error) {
	metadata := map[string]any{"name": name, "description": skill.Description}
	if len(skill.Tags) > 0 {
		metadata["tags"] = skill.Tags
	}
	if len(skill.Keywords) > 0 {
		metadata["keywords"] = skill.Keywords
	}
	if len(skill.Tools) > 0 {
		metadata["tools"] = skill.Tools
	}
	if len(skill.Arguments) > 0 {
		metadata["arguments"] = skill.Arguments
	}
	if len(skill.References) > 0 {
		metadata["references"] = skill.References
	}
	frontMatter, err := yaml.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	return []byte("---\n" + strings.TrimSpace(string(frontMatter)) + "\n---\n\n" + strings.TrimSpace(skill.Body) + "\n"), nil
}

func rewriteCompatibilitySkillManifestName(path, name string) error {
	directory, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	source, err := openRegularWorkspaceRootFile(directory, filepath.Base(path))
	if err != nil {
		return err
	}
	raw, readErr := io.ReadAll(io.LimitReader(source, maxSkillArchiveBytes+1))
	err = errors.Join(readErr, source.Close())
	if err != nil {
		return err
	}
	if len(raw) > maxSkillArchiveBytes {
		return errors.New("skill manifest is too large")
	}
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return errors.New("skill manifest is not valid UTF-8 text")
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return writeSkillFileAtomic(directory, filepath.Base(path), []byte("---\nname: "+name+"\n---\n\n"+text), false)
	}
	closing := strings.Index(text[4:], "\n---")
	if closing < 0 {
		return errors.New("skill manifest has unterminated frontmatter")
	}
	closing += 4
	frontMatter := text[4:closing]
	bodyStart := closing + len("\n---")
	body := strings.TrimPrefix(text[bodyStart:], "\n")
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(frontMatter), &document); err != nil {
		return err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("skill manifest frontmatter must be a mapping")
	}
	root := document.Content[0]
	updated := false
	for index := 0; index+1 < len(root.Content); index += 2 {
		if strings.EqualFold(strings.TrimSpace(root.Content[index].Value), "name") {
			root.Content[index+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}
			updated = true
			break
		}
	}
	if !updated {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "name"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
		)
	}
	encoded, err := yaml.Marshal(&document)
	if err != nil {
		return err
	}
	return writeSkillFileAtomic(directory, filepath.Base(path), []byte("---\n"+strings.TrimSpace(string(encoded))+"\n---\n"+body), false)
}

func (s *Server) editCompatibilitySkillFile(name, relativePath, oldString, newString string) (string, string, error) {
	return s.editCompatibilitySkillFileWithCommit(name, relativePath, oldString, newString, immediateSkillDraftCommit)
}

func (s *Server) editCompatibilitySkillFileWithCommit(name, relativePath, oldString, newString string, commit skillDraftCommit) (string, string, error) {
	if !utf8.ValidString(newString) || strings.IndexByte(newString, 0) >= 0 {
		return "", "", errors.New("Skill edit must be valid UTF-8 text without NUL")
	}
	skill, err := s.ensureCompatibilitySkillDraft(name, commit)
	if err != nil {
		return "", "", err
	}
	editableRoot, err := s.compatibilityEditableSkillRoot(skill)
	if err != nil {
		return "", "", err
	}
	directory, err := os.OpenRoot(editableRoot)
	if err != nil {
		return "", "", err
	}
	defer directory.Close()
	if err := verifyOpenedGrantedDirectory(directory, []hostGrant{{Path: filepath.Join(s.fileRoot, "skills"), Mode: "write"}}); err != nil {
		return "", "", err
	}
	relativePath, err = normalizeCompatibilitySkillResourcePath(relativePath)
	if err != nil {
		return "", "", err
	}
	target := filepath.Join(editableRoot, filepath.FromSlash(relativePath))
	if !pathWithinRoot(target, editableRoot) {
		return "", "", fmt.Errorf("invalid path '%s'", relativePath)
	}
	relativePath, err = filepath.Rel(editableRoot, target)
	if err != nil || !filepath.IsLocal(relativePath) {
		return "", "", errors.New("skill edit path escaped its root")
	}
	if oldString == "" {
		if _, err := directory.Lstat(relativePath); err == nil {
			return "", "", fmt.Errorf("file '%s' already exists in '%s' - read it first, then edit with a non-empty old_string", relativePath, name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
		if len(newString) > maxSkillArchiveBytes {
			return "", "", fmt.Errorf("skill file exceeds %d bytes", maxSkillArchiveBytes)
		}
		if err := commit(func() error {
			if err := ensureSkillEditParent(directory, filepath.Dir(relativePath)); err != nil {
				return err
			}
			if err := writeSkillFileAtomic(directory, relativePath, []byte(newString), true); err != nil {
				return err
			}
			if err := validateCompatibilitySkillDraftResources(directory); err != nil {
				if rollbackErr := directory.Remove(relativePath); rollbackErr != nil {
					return skillValidationError("draft_rollback_failed", "Skill draft resource failed safety checks and could not be rolled back", errors.Join(err, rollbackErr))
				}
				return err
			}
			return nil
		}); err != nil {
			return "", "", err
		}
		return "created", target, nil
	}
	source, err := openRegularWorkspaceRootFile(directory, relativePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("file '%s' does not exist in '%s' - use empty old_string to create it", relativePath, name)
		}
		return "", "", err
	}
	if err := validateCompatibilitySkillResourceIdentity(source); err != nil {
		_ = source.Close()
		return "", "", err
	}
	raw, readErr := io.ReadAll(io.LimitReader(source, maxSkillArchiveBytes+1))
	closeErr := source.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return "", "", err
	}
	if len(raw) > maxSkillArchiveBytes {
		return "", "", fmt.Errorf("skill file exceeds %d bytes", maxSkillArchiveBytes)
	}
	current := string(raw)
	matches := strings.Count(current, oldString)
	if matches == 0 {
		return "", "", errors.New("old_string not found - read the file first; the edit target may have changed")
	}
	if matches > 1 {
		return "", "", fmt.Errorf("old_string matches %d times - add more surrounding context so it matches exactly once", matches)
	}
	updated := strings.Replace(current, oldString, newString, 1)
	if len(updated) > maxSkillArchiveBytes {
		return "", "", fmt.Errorf("skill file exceeds %d bytes", maxSkillArchiveBytes)
	}
	if err := commit(func() error {
		if err := writeSkillFileAtomic(directory, relativePath, []byte(updated), false); err != nil {
			return err
		}
		if err := validateCompatibilitySkillDraftResources(directory); err != nil {
			return errors.Join(err, writeSkillFileAtomic(directory, relativePath, raw, false))
		}
		return nil
	}); err != nil {
		return "", "", err
	}
	return "edited", target, nil
}

func (s *Server) compatibilityEditableSkillRoot(skill skills.Skill) (string, error) {
	if strings.HasPrefix(skill.Path, "builtin:") {
		return "", fmt.Errorf("%w: built-in skills cannot be edited", errCompatibilitySkillReadOnly)
	}
	root, err := filepath.Abs(filepath.Dir(skill.Path))
	if err != nil {
		return "", err
	}
	writableRoot, err := filepath.Abs(filepath.Join(s.fileRoot, "skills"))
	if err != nil {
		return "", err
	}
	if !pathWithinRoot(root, writableRoot) {
		return "", fmt.Errorf("%w: skill '%s' cannot be edited in place; import or duplicate it into the writable skill catalog", errCompatibilitySkillReadOnly, skill.Name)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return "", errors.New("writable skill root is not a verified regular directory")
	}
	return root, nil
}
