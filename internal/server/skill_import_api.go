package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

const maxSkillImportRequestBytes = maxSkillArchiveBytes + (1 << 20)

var importedSkillNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type skillImportResult struct {
	Name          string
	InstalledPath string
	Files         []string
	Description   *string
}

func (s *Server) handleSkillBundleImport(w http.ResponseWriter, r *http.Request) {
	compatibility := r.URL.Path == "/api/skills/import"
	if r.Method != http.MethodPost {
		writeWorkspaceJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if s == nil || strings.TrimSpace(s.fileRoot) == "" || s.skillCatalog == nil {
		writeWorkspaceJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "writable skill catalog is not configured"})
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if compatibility && strings.HasPrefix(contentType, "application/json") {
		s.handleWebSkillPathImport(w, r)
		return
	}
	if compatibility && !strings.HasPrefix(contentType, "multipart/form-data") {
		writeWorkspaceJSON(w, http.StatusNotAcceptable, map[string]any{
			"detail": "the request is not multipart", "error": "multipart/form-data required",
		})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSkillImportRequestBytes)
	if err := r.ParseMultipartForm(maxSkillImportRequestBytes); err != nil {
		writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid skill bundle upload: " + err.Error()})
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		if compatibility {
			writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"error": "missing file"})
		} else {
			writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "skill bundle file is required"})
		}
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxSkillArchiveBytes+1))
	if err != nil {
		writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "read skill bundle: " + err.Error()})
		return
	}
	if len(raw) == 0 || len(raw) > maxSkillArchiveBytes {
		writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("skill bundle must be between 1 and %d bytes", maxSkillArchiveBytes)})
		return
	}
	var record func(skillImportResult) error
	if compatibility {
		record = func(result skillImportResult) error {
			entry := newWebSkillImportHistory(uuid.NewString(), "File upload", "", header.Filename, result.Name, "imported", "")
			entry.ActualBytes = int64(len(raw))
			return s.recordSkillImports(compatAgentUserID(r), nil, []webSkillImportHistoryEntry{entry})
		}
	}
	result, err := s.importSkillBundleWithRecord(header.Filename, strings.TrimSpace(r.FormValue("name")), parseImportReplace(r.FormValue("replace")), raw, record)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, os.ErrExist) {
			status = http.StatusConflict
		}
		if errors.Is(err, errSkillImportMetadata) {
			status = http.StatusInternalServerError
		}
		writeWorkspaceJSON(w, status, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	status := http.StatusOK
	if compatibility {
		status = http.StatusCreated
	}
	writeWorkspaceJSON(w, status, map[string]any{
		"ok": true, "imported": true, "name": result.Name,
		"skill_name": result.Name, "skill_names": []string{result.Name},
		"installed_to": filepath.ToSlash(result.InstalledPath), "files": result.Files, "description": result.Description,
	})
}

func (s *Server) handleWebSkillPathImport(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SkillPath  string `json:"skill_path"`
		LegacyPath string `json:"skillPath"`
		Replace    bool   `json:"replace"`
	}
	if err := decodeAgentCompatJSON(r, &input); err != nil {
		writeV11Detail(w, http.StatusBadRequest, "Invalid skill import request: "+err.Error())
		return
	}
	requested := strings.TrimSpace(firstNonEmpty(input.SkillPath, input.LegacyPath))
	items, canonical, err := s.loadGrantedWebSkills(compatAgentUserID(r), requested)
	if err != nil {
		writeV11Detail(w, webSkillPathStatus(err), err.Error())
		return
	}
	operationID := uuid.NewString()
	prepared := make([]*preparedSkillImport, 0, len(items))
	defer func() {
		for _, item := range prepared {
			item.cleanup()
		}
	}()
	failed := make([]map[string]any, 0)
	history := make([]webSkillImportHistoryEntry, 0, len(items))
	addFailure := func(name, path string, err error) {
		code := "IMPORT_FAILED"
		if errors.Is(err, os.ErrExist) {
			code = "ALREADY_EXISTS"
		}
		failed = append(failed, map[string]any{"source_name": name, "code": code, "error_path": path})
		history = append(history, newWebSkillImportHistory(operationID, filepath.Base(canonical), canonical, name, name, "failed", code))
	}
	for _, skill := range items {
		raw, err := workspaceSkillArchive(skill)
		var item *preparedSkillImport
		if err == nil {
			item, err = s.prepareSkillBundle(skill.Name+".zip", "", input.Replace, raw)
		}
		if err != nil {
			addFailure(skill.Name, skill.Path, err)
			continue
		}
		prepared = append(prepared, item)
	}
	results, _, err := s.publishPreparedSkillImports(prepared, immediateSkillDraftCommit, func(results []skillImportResult, conflicts []skillImportFailure) error {
		for _, conflict := range conflicts {
			for _, skill := range items {
				if strings.EqualFold(skill.Name, conflict.name) {
					addFailure(conflict.name, skill.Path, conflict.err)
					break
				}
			}
		}
		for _, result := range results {
			history = append(history, newWebSkillImportHistory(operationID, filepath.Base(canonical), canonical, result.Name, result.Name, "imported", ""))
		}
		return s.recordSkillImports(compatAgentUserID(r), nil, history)
	})
	if err != nil {
		writeV11Detail(w, http.StatusInternalServerError, "skill import could not be committed: "+err.Error())
		return
	}
	imported := make([]string, 0, len(results))
	for _, result := range results {
		imported = append(imported, result.Name)
	}
	if len(imported) == 0 {
		writeWorkspaceJSON(w, http.StatusConflict, map[string]any{
			"skill_name": "", "skill_names": []string{}, "failed": failed,
		})
		return
	}
	sort.Strings(imported)
	writeWorkspaceJSON(w, http.StatusCreated, map[string]any{
		"skill_name": imported[0], "skill_names": imported, "failed": failed,
	})
}

func findCaseInsensitiveSkillDestination(root, name string) (string, bool, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", false, fmt.Errorf("list installed skills: %w", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".skill-import-") || strings.Contains(entry.Name(), ".backup-") {
			continue
		}
		if strings.EqualFold(entry.Name(), name) {
			return entry.Name(), true, nil
		}
	}
	return "", false, nil
}

func skillManifestHasName(raw []byte) bool {
	return skillManifestHasStringField(raw, "name")
}

func skillManifestHasDescription(raw []byte) bool {
	return skillManifestHasStringField(raw, "description")
}

func skillManifestHasStringField(raw []byte, field string) bool {
	lines := strings.Split(string(raw), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return false
	}
	for index, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			var metadata map[string]any
			if err := yaml.Unmarshal([]byte(strings.Join(lines[1:index+1], "\n")), &metadata); err != nil {
				return false
			}
			for key, value := range metadata {
				if strings.EqualFold(strings.TrimSpace(key), field) {
					text, ok := value.(string)
					return ok && strings.TrimSpace(text) != ""
				}
			}
			return false
		}
	}
	return false
}

func ensurePrivateDirectoryForImport(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(path, 0o700)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("skill root must be a regular directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return os.Chmod(path, 0o700)
	}
	return nil
}

func pathWithinRoot(candidate, root string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func parseImportReplace(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "1" || value == "true" || value == "yes"
}
