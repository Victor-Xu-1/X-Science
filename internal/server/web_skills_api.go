package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"x-science/internal/skills"
)

const (
	webSkillExternalPathsSetting = "skills.externalPaths"
	webSkillImportHistorySetting = "skills.importHistory"
	maxWebSkillExternalPaths     = 32
	maxWebSkillImportHistory     = 500
)

type webSkillExternalPath struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type webSkillImportHistoryEntry struct {
	ID          string `json:"id"`
	OperationID string `json:"operation_id"`
	SourceLabel string `json:"source_label"`
	SourcePath  string `json:"source_path,omitempty"`
	SourceName  string `json:"source_name"`
	SkillID     string `json:"skill_id,omitempty"`
	SkillName   string `json:"skill_name,omitempty"`
	Status      string `json:"status"`
	ErrorCode   string `json:"error_code,omitempty"`
	ErrorPath   string `json:"error_path,omitempty"`
	ActualBytes int64  `json:"actual_bytes,omitempty"`
	LimitBytes  int64  `json:"limit_bytes,omitempty"`
	Line        int    `json:"line,omitempty"`
	Column      int    `json:"column,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

func (s *Server) handleWebSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if s.skillCatalog == nil {
		writeV11Detail(w, http.StatusServiceUnavailable, "Skill catalog is not configured")
		return
	}
	userID := compatAgentUserID(r)
	if err := s.syncWebExternalSkills(r.Context(), userID); err != nil {
		writeV11Detail(w, http.StatusInternalServerError, "failed to load external skills")
		return
	}
	entries := make([]map[string]any, 0, len(s.skillCatalog.Skills()))
	for _, skill := range s.skillCatalog.Skills() {
		location := skill.Path
		relative := ""
		if strings.TrimSpace(s.fileRoot) != "" && !strings.HasPrefix(skill.Path, "builtin:") {
			if value, err := filepath.Rel(s.fileRoot, skill.Path); err == nil && !strings.HasPrefix(value, "..") {
				relative = filepath.ToSlash(value)
			}
		}
		source := webSkillBridgeSource(s, skill)
		entries = append(entries, map[string]any{
			"name": skill.Name, "description": skill.Description, "description_i18n": skill.DescriptionI18n,
			"category": skill.Category, "location": location,
			"relative_location": relative, "is_auto_inject": compatibilityRequiredPlatformSkill(skill),
			"is_custom": source == "custom", "source": source,
		})
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleWebSkillMaterialize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if s.workspaceStore == nil {
		writeV11Detail(w, http.StatusServiceUnavailable, "Workspace store is not configured")
		return
	}
	var input struct {
		ConversationID string   `json:"conversation_id"`
		Skills         []string `json:"skills"`
	}
	if err := decodeAgentCompatJSON(r, &input); err != nil {
		writeV11Detail(w, http.StatusBadRequest, "Invalid skill materialization request: "+err.Error())
		return
	}
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	if input.ConversationID == "" || len(input.Skills) > 256 {
		writeV11Detail(w, http.StatusBadRequest, "conversation_id and at most 256 skills are required")
		return
	}
	frame, found, err := s.workspaceStore.GetCompatibilityFrame(input.ConversationID)
	if err != nil || !found {
		writeV11Detail(w, http.StatusNotFound, "Conversation not found")
		return
	}
	owned, err := s.workspaceStore.ProjectOwnedBy(frame.ProjectID, compatAgentUserID(r))
	if err != nil {
		writeV11StoreError(w, err)
		return
	}
	if !owned {
		writeV11Detail(w, http.StatusNotFound, "Conversation not found")
		return
	}
	materialized := make([]map[string]any, 0, len(input.Skills))
	seen := map[string]bool{}
	for _, name := range input.Skills {
		name = strings.TrimSpace(name)
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			continue
		}
		seen[key] = true
		skill, ok := findCatalogSkill(s.skillCatalog, name)
		if !ok || !s.runtimeSkillEnabled(skill.Name) {
			writeV11Detail(w, http.StatusBadRequest, fmt.Sprintf("skill '%s' is unavailable", name))
			return
		}
		materialized = append(materialized, map[string]any{"name": skill.Name, "source_path": skill.Path})
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": materialized})
}

func (s *Server) handleWebSkillInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var input struct {
		SkillPath string `json:"skill_path"`
	}
	if err := decodeAgentCompatJSON(r, &input); err != nil {
		writeV11Detail(w, http.StatusBadRequest, "Invalid skill info request: "+err.Error())
		return
	}
	items, _, err := s.loadGrantedWebSkills(r.Context(), compatAgentUserID(r), input.SkillPath)
	if err != nil {
		writeV11Detail(w, webSkillPathStatus(err), err.Error())
		return
	}
	if len(items) != 1 {
		writeV11Detail(w, http.StatusBadRequest, fmt.Sprintf("skill_path must contain exactly one skill, found %d", len(items)))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": items[0].Name, "description": items[0].Description})
}

func (s *Server) handleWebSkillScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var input struct {
		FolderPath string `json:"folder_path"`
	}
	if err := decodeAgentCompatJSON(r, &input); err != nil {
		writeV11Detail(w, http.StatusBadRequest, "Invalid skill scan request: "+err.Error())
		return
	}
	items, _, err := s.loadGrantedWebSkills(r.Context(), compatAgentUserID(r), input.FolderPath)
	if err != nil {
		writeV11Detail(w, webSkillPathStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, projectScannedWebSkills(items))
}

func (s *Server) handleWebSkillPaths(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	userRoot := ""
	if strings.TrimSpace(s.fileRoot) != "" {
		userRoot = filepath.Join(s.fileRoot, "skills")
	}
	builtinRoot := ""
	for _, root := range s.skillDirectories {
		if !sameCleanPath(root, userRoot) {
			builtinRoot = root
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_skills_dir": userRoot, "builtin_skills_dir": builtinRoot})
}

func (s *Server) handleWebSkillImportLimits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"max_file_bytes": maxSkillArchiveBytes, "max_total_bytes": maxSkillArchiveBytes,
	})
}

func (s *Server) handleWebSkillDetectPaths(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, webCommonSkillPaths())
}

func (s *Server) handleWebSkillDetectExternal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	userID := compatAgentUserID(r)
	paths, err := s.loadWebSkillExternalPaths(userID)
	if err != nil {
		writeV11StoreError(w, err)
		return
	}
	result := make([]map[string]any, 0, len(paths))
	for _, source := range paths {
		items, canonical, loadErr := s.loadGrantedWebSkills(r.Context(), userID, source.Path)
		if loadErr != nil {
			continue
		}
		result = append(result, map[string]any{
			"name": source.Name, "source": source.Name, "path": canonical,
			"count": len(items), "skills": projectScannedWebSkills(items),
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleWebSkillExternalPaths(w http.ResponseWriter, r *http.Request) {
	userID := compatAgentUserID(r)
	switch r.Method {
	case http.MethodGet:
		paths, err := s.loadWebSkillExternalPaths(userID)
		if err != nil {
			writeV11StoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, paths)
	case http.MethodPost:
		var input webSkillExternalPath
		if err := decodeAgentCompatJSON(r, &input); err != nil {
			writeV11Detail(w, http.StatusBadRequest, "Invalid external skill path: "+err.Error())
			return
		}
		input.Name = strings.TrimSpace(input.Name)
		if input.Name == "" || len(input.Name) > 128 {
			writeV11Detail(w, http.StatusBadRequest, "external skill path name is required")
			return
		}
		snapshot := s.skillCatalog.Snapshot()
		items, canonical, err := s.loadGrantedWebSkills(r.Context(), userID, input.Path)
		if err != nil {
			writeV11Detail(w, webSkillPathStatus(err), err.Error())
			return
		}
		input.Path = canonical
		paths, err := s.loadWebSkillExternalPaths(userID)
		if err != nil {
			writeV11StoreError(w, err)
			return
		}
		updated := false
		for index := range paths {
			if sameCleanPath(paths[index].Path, canonical) {
				paths[index] = input
				updated = true
				break
			}
		}
		if !updated {
			if len(paths) >= maxWebSkillExternalPaths {
				writeV11Detail(w, http.StatusBadRequest, fmt.Sprintf("at most %d external skill paths are allowed", maxWebSkillExternalPaths))
				return
			}
			paths = append(paths, input)
		}
		sort.Slice(paths, func(i, j int) bool { return strings.ToLower(paths[i].Name) < strings.ToLower(paths[j].Name) })
		if err := s.saveWebSkillExternalPaths(userID, paths); err != nil {
			writeV11StoreError(w, err)
			return
		}
		changed, refreshErr := s.compareApplyExternalWebSkills(r.Context(), snapshot, [][]skills.Skill{items})
		// Saving the source registration already succeeded. A stale or canceled
		// content refresh cannot roll it back or imply that old bytes were accepted.
		if refreshErr != nil {
			s.notifySkillCatalogChanged(userID)
			writeJSON(w, http.StatusOK, nil)
			return
		}
		if changed {
			s.notifySkillCatalogChanged("")
		} else {
			s.notifySkillCatalogChanged(userID)
		}
		writeJSON(w, http.StatusOK, nil)
	case http.MethodDelete:
		requested := strings.TrimSpace(r.URL.Query().Get("path"))
		if requested == "" && r.Body != nil {
			var input struct {
				Path string `json:"path"`
			}
			if err := decodeAgentCompatJSON(r, &input); err == nil {
				requested = strings.TrimSpace(input.Path)
			}
		}
		canonical, err := canonicalHostDirectory(requested)
		if err != nil {
			writeV11Detail(w, http.StatusBadRequest, err.Error())
			return
		}
		paths, err := s.loadWebSkillExternalPaths(userID)
		if err != nil {
			writeV11StoreError(w, err)
			return
		}
		next := make([]webSkillExternalPath, 0, len(paths))
		removed := false
		for _, source := range paths {
			if sameCleanPath(source.Path, canonical) {
				removed = true
				continue
			}
			next = append(next, source)
		}
		if removed {
			if err := s.saveWebSkillExternalPaths(userID, next); err != nil {
				writeV11StoreError(w, err)
				return
			}
			if len(s.skillCatalog.RemoveSkillsUnder(canonical)) > 0 {
				s.notifySkillCatalogChanged("")
			} else {
				s.notifySkillCatalogChanged(userID)
			}
		}
		writeJSON(w, http.StatusOK, nil)
	default:
		writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *Server) handleWebSkillImportHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	history, err := s.loadWebSkillImportHistory(compatAgentUserID(r))
	if err != nil {
		writeV11StoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func (s *Server) handleWebBuiltinSkill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var input struct {
		FileName string `json:"file_name"`
	}
	if err := decodeAgentCompatJSON(r, &input); err != nil {
		writeV11Detail(w, http.StatusBadRequest, "Invalid built-in skill request")
		return
	}
	name := strings.TrimSuffix(filepath.Base(strings.TrimSpace(input.FileName)), filepath.Ext(input.FileName))
	skill, found := findCatalogSkill(s.skillCatalog, name)
	if !found {
		writeV11Detail(w, http.StatusNotFound, "Built-in skill not found")
		return
	}
	content, err := compatibilityCatalogSkillContent(skill, "SKILL.md")
	if err != nil {
		writeV11Detail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, content)
}

func (s *Server) handleWebBuiltinRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var input struct {
		FileName string `json:"file_name"`
	}
	if err := decodeAgentCompatJSON(r, &input); err != nil {
		writeV11Detail(w, http.StatusBadRequest, "Invalid built-in rule request")
		return
	}
	name := strings.TrimSuffix(filepath.Base(strings.TrimSpace(input.FileName)), filepath.Ext(input.FileName))
	name = normalizeBundledAgentName(name)
	if s.agentCatalog == nil {
		writeV11Detail(w, http.StatusServiceUnavailable, "Built-in rule catalog is not configured")
		return
	}
	agent, found := s.agentCatalog.Agent(name)
	if !found {
		writeV11Detail(w, http.StatusNotFound, "Built-in rule not found")
		return
	}
	writeJSON(w, http.StatusOK, agent.EffectiveSystemPrompt())
}

func (s *Server) loadGrantedWebSkills(ctx context.Context, userID, requested string) ([]skills.Skill, string, error) {
	if ctx == nil {
		return nil, "", skills.ErrNilLoadContext
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if s == nil || s.settingsStore == nil {
		return nil, "", errors.New("settings store is not configured")
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return nil, "", errors.New("skill path is required")
	}
	absolute, err := filepath.Abs(requested)
	if err != nil {
		return nil, "", err
	}
	grants, err := s.loadHostGrants(userID)
	if err != nil {
		return nil, "", err
	}
	allowedPath := func(path string) bool {
		for _, grant := range grants {
			if hostPathWithin(grant.Path, path) {
				return true
			}
		}
		return false
	}
	// Do not reveal host-path existence or type before verifying the grant.
	if !allowedPath(absolute) {
		return nil, "", errWebSkillPathForbidden
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, "", errors.New("skill path does not exist")
	}
	if !allowedPath(resolved) {
		return nil, "", errWebSkillPathForbidden
	}
	var parent *os.Root
	var relative string
	for _, grant := range grants {
		if !hostPathWithin(grant.Path, resolved) {
			continue
		}
		parent, err = os.OpenRoot(grant.Path)
		if err != nil {
			return nil, "", err
		}
		if err := verifyOpenedGrantedDirectory(parent, []hostGrant{grant}); err != nil {
			_ = parent.Close()
			return nil, "", errWebSkillPathForbidden
		}
		relative, err = filepath.Rel(grant.Path, resolved)
		break
	}
	if parent == nil {
		return nil, "", errWebSkillPathForbidden
	}
	defer parent.Close()
	if err != nil || !filepath.IsLocal(relative) {
		return nil, "", errWebSkillPathForbidden
	}
	info, err := parent.Stat(relative)
	if err != nil {
		return nil, "", err
	}
	root := resolved
	if !info.IsDir() {
		if !info.Mode().IsRegular() || !strings.EqualFold(filepath.Base(resolved), "SKILL.md") {
			return nil, "", errors.New("skill path must be a directory or SKILL.md")
		}
		root = filepath.Dir(resolved)
		relative = filepath.Dir(relative)
	}
	if !allowedPath(root) {
		return nil, "", errWebSkillPathForbidden
	}
	directory, err := parent.OpenRoot(relative)
	if err != nil {
		return nil, "", err
	}
	defer directory.Close()
	if err := verifyOpenedGrantedDirectory(directory, grants); err != nil {
		return nil, "", errWebSkillPathForbidden
	}
	loaded, err := skills.LoadFromRootContext(ctx, directory, root, runtimeSkillLoadOptions())
	if err != nil {
		return nil, "", fmt.Errorf("invalid skill source: %w", err)
	}
	if failures := loaded.LoadErrors(); len(failures) > 0 {
		return nil, "", fmt.Errorf("invalid skill source: %s", failures[0].Err)
	}
	items := loaded.Skills()
	if len(items) == 0 {
		return nil, "", errors.New("skill source contains no SKILL.md")
	}
	if len(items) > 100 {
		return nil, "", errors.New("skill source contains more than 100 skills")
	}
	return items, root, nil
}

var errWebSkillPathForbidden = errors.New("skill path is outside granted host directories")

func webSkillPathStatus(err error) int {
	if errors.Is(err, errWebSkillPathForbidden) {
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}

func projectScannedWebSkills(items []skills.Skill) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, skill := range items {
		result = append(result, map[string]any{
			"name": skill.Name, "description": skill.Description, "path": filepath.Dir(skill.Path),
			"source": "extension", "location": skill.Path, "is_auto_inject": false,
		})
	}
	return result
}

func webCommonSkillPaths() []webSkillExternalPath {
	home, err := os.UserHomeDir()
	if err != nil {
		return []webSkillExternalPath{}
	}
	candidates := []webSkillExternalPath{
		{Name: "Claude", Path: filepath.Join(home, ".claude", "skills")},
		{Name: "Codex", Path: filepath.Join(home, ".codex", "skills")},
		{Name: "X-Science", Path: filepath.Join(home, ".x-science", "skills")},
	}
	result := make([]webSkillExternalPath, 0, len(candidates))
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate.Path); err == nil && info.IsDir() {
			result = append(result, candidate)
		}
	}
	return result
}

func (s *Server) syncWebExternalSkills(ctx context.Context, userID string) error {
	return s.syncWebExternalSkillsWithLoader(ctx, userID, s.loadGrantedWebSkills)
}

func (s *Server) syncWebExternalSkillsWithLoader(ctx context.Context, userID string, load func(context.Context, string, string) ([]skills.Skill, string, error)) error {
	if ctx == nil {
		return skills.ErrNilLoadContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshot := s.skillCatalog.Snapshot()
	paths, err := s.loadWebSkillExternalPaths(userID)
	if err != nil {
		return err
	}
	loadedSources := make([][]skills.Skill, 0, len(paths))
	for _, source := range paths {
		items, _, loadErr := load(ctx, userID, source.Path)
		if err := ctx.Err(); err != nil {
			return err
		}
		if loadErr != nil {
			// Keep this source's last known projection; an independent complete
			// source must not be blocked by another root's failure.
			continue
		}
		loadedSources = append(loadedSources, items)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	catalogChanged, err := s.compareApplyExternalWebSkills(ctx, snapshot, loadedSources)
	if err != nil {
		return err
	}
	if catalogChanged {
		s.notifySkillCatalogChanged("")
	}
	return nil
}

func (s *Server) compareApplyExternalWebSkills(ctx context.Context, snapshot skills.CatalogSnapshot, sources [][]skills.Skill) (bool, error) {
	if ctx == nil {
		return false, skills.ErrNilLoadContext
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	upserts := externalWebSkillUpserts(snapshot.Skills, sources)
	// Match the publisher's lock order. No filesystem, settings or event IO is
	// performed in this short content commit; all source reads are complete.
	// A publisher can own the guard during file/DB work. Catalog reads must not
	// wait for that work: keep the current projection and let a later read scan.
	if !s.skillMutationMu.TryLock() {
		return false, ctx.Err()
	}
	changed, err := s.skillCatalog.CompareApply(ctx, snapshot.Revision, upserts)
	s.skillMutationMu.Unlock()
	if errors.Is(err, skills.ErrCatalogRevisionConflict) {
		// Another accepted mutation won. Serve the current catalog without
		// turning a normal GET into an error or a self-triggered retry loop.
		return false, nil
	}
	return changed, err
}

// Build the original first-visible-name/path plan on the owned snapshot, not
// on live Catalog reads between individual writes. This is transient planning
// data, and only CompareApply can publish it to the original Catalog.
func externalWebSkillUpserts(projected []skills.Skill, sources [][]skills.Skill) []skills.Skill {
	upserts := make([]skills.Skill, 0)
	for _, items := range sources {
		for _, skill := range items {
			conflict := false
			for _, existing := range projected {
				if strings.EqualFold(strings.TrimSpace(existing.Name), strings.TrimSpace(skill.Name)) {
					conflict = !sameCleanPath(existing.Path, skill.Path)
					break
				}
			}
			if conflict {
				continue
			}
			upserts = append(upserts, skill)
			replaced := false
			for index := range projected {
				if strings.EqualFold(projected[index].Name, skill.Name) {
					projected[index], replaced = skill, true
					break
				}
			}
			if !replaced {
				projected = append(projected, skill)
			}
			sort.Slice(projected, func(i, j int) bool { return strings.ToLower(projected[i].Name) < strings.ToLower(projected[j].Name) })
		}
	}
	return upserts
}

func webSkillBridgeSource(s *Server, skill skills.Skill) string {
	if compatibilityRequiredPlatformSkill(skill) || s.compatibilitySkillSource(skill) == "x_science_llm" {
		return "builtin"
	}
	if s != nil && strings.TrimSpace(s.fileRoot) != "" {
		root := filepath.Join(s.fileRoot, "skills")
		if absolute, err := filepath.Abs(skill.Path); err == nil && pathWithinRoot(absolute, root) {
			return "custom"
		}
	}
	return "extension"
}

func (s *Server) loadWebSkillExternalPaths(userID string) ([]webSkillExternalPath, error) {
	if s.settingsStore == nil {
		return []webSkillExternalPath{}, errors.New("settings store is not configured")
	}
	setting, found, err := s.settingsStore.Get(webSkillUserSettingKey(webSkillExternalPathsSetting, userID))
	if err != nil || !found {
		return []webSkillExternalPath{}, err
	}
	raw, err := json.Marshal(setting.Value)
	if err != nil {
		return nil, err
	}
	paths := []webSkillExternalPath{}
	if err := json.Unmarshal(raw, &paths); err != nil {
		return nil, err
	}
	return paths, nil
}

func (s *Server) saveWebSkillExternalPaths(userID string, paths []webSkillExternalPath) error {
	_, err := s.settingsStore.Set(webSkillUserSettingKey(webSkillExternalPathsSetting, userID), paths)
	return err
}

func (s *Server) loadWebSkillImportHistory(userID string) ([]webSkillImportHistoryEntry, error) {
	if s.settingsStore == nil {
		return []webSkillImportHistoryEntry{}, errors.New("settings store is not configured")
	}
	setting, found, err := s.settingsStore.Get(webSkillUserSettingKey(webSkillImportHistorySetting, userID))
	if err != nil || !found {
		return []webSkillImportHistoryEntry{}, err
	}
	raw, err := json.Marshal(setting.Value)
	if err != nil {
		return nil, err
	}
	history := []webSkillImportHistoryEntry{}
	if err := json.Unmarshal(raw, &history); err != nil {
		return nil, err
	}
	return history, nil
}

func (s *Server) appendWebSkillImportHistory(userID string, entries ...webSkillImportHistoryEntry) error {
	if len(entries) == 0 || s.settingsStore == nil {
		return nil
	}
	key := webSkillUserSettingKey(webSkillImportHistorySetting, userID)
	_, err := s.settingsStore.Update(key, func(current any, found bool) (any, error) {
		history := []webSkillImportHistoryEntry{}
		if found {
			raw, err := json.Marshal(current)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(raw, &history); err != nil {
				return nil, err
			}
		}
		history = append(entries, history...)
		if len(history) > maxWebSkillImportHistory {
			history = history[:maxWebSkillImportHistory]
		}
		return history, nil
	})
	return err
}

func newWebSkillImportHistory(operationID, sourceLabel, sourcePath, sourceName, skillName, status, code string) webSkillImportHistoryEntry {
	return webSkillImportHistoryEntry{
		ID: uuid.NewString(), OperationID: operationID, SourceLabel: sourceLabel,
		SourcePath: sourcePath, SourceName: sourceName, SkillID: skillName,
		SkillName: skillName, Status: status, ErrorCode: code, CreatedAt: time.Now().UTC().UnixMilli(),
	}
}

func webSkillUserSettingKey(prefix, userID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(userID)))
	return prefix + "." + hex.EncodeToString(digest[:8])
}

func sameCleanPath(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && strings.EqualFold(filepath.Clean(leftAbs), filepath.Clean(rightAbs))
}
