package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"x-science/internal/buildinfo"
	"x-science/internal/skills"
)

const (
	webSkillMarketplaceSourcesSetting = "skills.marketplaceSources"
	maxMarketplaceArchiveBytes        = 50 << 20
	maxMarketplaceExtractedBytes      = 100 << 20
	maxMarketplaceArchiveFiles        = 5000
	maxMarketplaceSkills              = 100
)

var (
	githubRepositoryPartPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,99})$`)
	githubCommitPattern         = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
)

type webSkillMarketplaceSource struct {
	Slug           string                                      `json:"slug"`
	Repo           string                                      `json:"repo"`
	SHA            string                                      `json:"sha"`
	License        *string                                     `json:"license"`
	Skills         []string                                    `json:"skills"`
	ImportedAt     string                                      `json:"imported_at"`
	Removable      bool                                        `json:"removable"`
	Hashes         map[string]string                           `json:"hashes,omitempty"`
	SkillRevisions map[string]webSkillMarketplaceSkillRevision `json:"skill_revisions,omitempty"`
}

type githubSkillRepository struct {
	Owner string
	Name  string
}

func (repository githubSkillRepository) Canonical() string {
	return "https://github.com/" + repository.Owner + "/" + repository.Name
}

func (repository githubSkillRepository) Slug() string {
	return strings.ToLower(repository.Owner + "-" + repository.Name)
}

func (s *Server) handleWebSkillMarketplacePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var input struct {
		Repo string `json:"repo"`
	}
	if err := decodeAgentCompatJSON(r, &input); err != nil {
		writeV11Detail(w, http.StatusBadRequest, "Invalid marketplace preview request: "+err.Error())
		return
	}
	repository, err := parseGitHubSkillRepository(input.Repo)
	if err != nil {
		writeV11Detail(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	sha, err := s.resolveGitHubSkillCommit(ctx, repository, "HEAD")
	if err != nil {
		writeV11Detail(w, http.StatusBadGateway, err.Error())
		return
	}
	license, _ := s.resolveGitHubSkillLicense(ctx, repository, sha)
	items, cleanup, err := s.downloadGitHubSkillRepository(ctx, repository, sha)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		writeV11Detail(w, http.StatusBadGateway, err.Error())
		return
	}
	projected := make([]map[string]any, 0, len(items))
	for _, skill := range items {
		projected = append(projected, map[string]any{
			"name": skill.Name, "displayName": skill.Name, "description": skill.Description,
			"path": filepath.ToSlash(skill.Path), "selected": true,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"repo": repository.Canonical(), "slug": repository.Slug(), "sha": sha,
		"license": license, "skills": projected,
	})
}

func (s *Server) handleWebSkillMarketplaceImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var input skillMarketplaceImportInput
	if err := decodeAgentCompatJSON(r, &input); err != nil {
		writeV11Detail(w, http.StatusBadRequest, "Invalid marketplace import request: "+err.Error())
		return
	}
	result, err := s.importMarketplaceSkills(r.Context(), compatAgentUserID(r), input, immediateSkillDraftCommit)
	if err != nil {
		status := http.StatusInternalServerError
		var failure *skillMarketplaceImportError
		if errors.As(err, &failure) {
			status = failure.status
		}
		writeV11Detail(w, status, err.Error())
		return
	}
	status := http.StatusCreated
	if len(result.imported) == 0 {
		status = http.StatusConflict
	}
	writeJSON(w, status, result.projection())
}

func (s *Server) handleWebSkillMarketplaceSources(w http.ResponseWriter, r *http.Request) {
	userID := compatAgentUserID(r)
	if r.URL.Path == "/api/marketplace/sources" {
		if r.Method != http.MethodGet {
			writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		sources, err := s.loadWebSkillMarketplaceSources(userID)
		if err != nil {
			writeV11StoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sources": sources})
		return
	}
	if r.Method != http.MethodDelete {
		writeV11Detail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	rawSlug := strings.TrimPrefix(r.URL.Path, "/api/marketplace/sources/")
	slug, err := url.PathUnescape(rawSlug)
	if err != nil || strings.TrimSpace(slug) == "" {
		writeV11Detail(w, http.StatusBadRequest, "Invalid marketplace source")
		return
	}
	if err := s.removeWebSkillMarketplaceSource(userID, strings.TrimSpace(slug)); err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		writeV11Detail(w, status, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseGitHubSkillRepository(value string) (githubSkillRepository, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return githubSkillRepository{}, errors.New("GitHub repository is required")
	}
	if !strings.Contains(value, "://") {
		parts := strings.Split(strings.TrimSuffix(value, ".git"), "/")
		if len(parts) != 2 {
			return githubSkillRepository{}, errors.New("repository must be owner/name or an HTTPS github.com URL")
		}
		return validateGitHubSkillRepository(parts[0], parts[1])
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return githubSkillRepository{}, errors.New("only public HTTPS github.com repositories are supported")
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(parsed.Path, ".git"), "/"), "/")
	if len(parts) != 2 {
		return githubSkillRepository{}, errors.New("GitHub repository URL must identify exactly owner/name")
	}
	return validateGitHubSkillRepository(parts[0], parts[1])
}

func validateGitHubSkillRepository(owner, name string) (githubSkillRepository, error) {
	owner = strings.TrimSpace(owner)
	name = strings.TrimSpace(name)
	if !githubRepositoryPartPattern.MatchString(owner) || !githubRepositoryPartPattern.MatchString(name) || owner == "." || name == "." {
		return githubSkillRepository{}, errors.New("GitHub owner and repository names are invalid")
	}
	return githubSkillRepository{Owner: owner, Name: name}, nil
}

func (s *Server) resolveGitHubSkillCommit(ctx context.Context, repository githubSkillRepository, revision string) (string, error) {
	var payload struct {
		SHA string `json:"sha"`
	}
	path := fmt.Sprintf("/repos/%s/%s/commits/%s", url.PathEscape(repository.Owner), url.PathEscape(repository.Name), url.PathEscape(revision))
	if err := s.githubSkillJSON(ctx, path, &payload); err != nil {
		return "", fmt.Errorf("resolve GitHub commit: %w", err)
	}
	payload.SHA = strings.ToLower(strings.TrimSpace(payload.SHA))
	if !githubCommitPattern.MatchString(payload.SHA) {
		return "", errors.New("GitHub returned an invalid commit identifier")
	}
	return payload.SHA, nil
}

func (s *Server) resolveGitHubSkillLicense(ctx context.Context, repository githubSkillRepository, revision string) (*string, error) {
	var payload struct {
		License struct {
			SPDXID string `json:"spdx_id"`
		} `json:"license"`
	}
	path := fmt.Sprintf("/repos/%s/%s/license?ref=%s", url.PathEscape(repository.Owner), url.PathEscape(repository.Name), url.QueryEscape(revision))
	if err := s.githubSkillJSON(ctx, path, &payload); err != nil {
		return nil, err
	}
	value := strings.TrimSpace(payload.License.SPDXID)
	if value == "" || strings.EqualFold(value, "NOASSERTION") {
		return nil, nil
	}
	return &value, nil
}

func (s *Server) githubSkillJSON(ctx context.Context, path string, output any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com"+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", buildinfo.UserAgent())
	response, err := s.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GitHub API returned %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if err := decoder.Decode(output); err != nil {
		return err
	}
	return nil
}

func (s *Server) downloadGitHubSkillRepository(
	ctx context.Context,
	repository githubSkillRepository,
	sha string,
) ([]skills.Skill, func(), error) {
	archiveURL := fmt.Sprintf("https://codeload.github.com/%s/%s/zip/%s", url.PathEscape(repository.Owner), url.PathEscape(repository.Name), sha)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("User-Agent", buildinfo.UserAgent())
	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("GitHub archive returned %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxMarketplaceArchiveBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(raw) == 0 || len(raw) > maxMarketplaceArchiveBytes {
		return nil, nil, errors.New("GitHub archive exceeds the 50MB download limit")
	}
	staging, err := os.MkdirTemp("", "x-science-skill-marketplace-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(staging) }
	if err := extractMarketplaceSkillArchive(staging, raw); err != nil {
		cleanup()
		return nil, nil, err
	}
	loaded := skills.Load([]string{staging}, runtimeSkillLoadOptions())
	if failures := loaded.LoadErrors(); len(failures) > 0 {
		cleanup()
		return nil, nil, fmt.Errorf("repository contains an invalid skill: %s", failures[0].Err)
	}
	items := loaded.Skills()
	if len(items) == 0 || len(items) > maxMarketplaceSkills {
		cleanup()
		return nil, nil, fmt.Errorf("repository must contain between 1 and %d skills", maxMarketplaceSkills)
	}
	return items, cleanup, nil
}

func extractMarketplaceSkillArchive(destination string, raw []byte) error {
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return fmt.Errorf("open GitHub archive: %w", err)
	}
	if len(archive.File) > maxMarketplaceArchiveFiles {
		return fmt.Errorf("GitHub archive exceeds %d files", maxMarketplaceArchiveFiles)
	}
	seen := map[string]bool{}
	var total int64
	rootPrefix := ""
	for _, entry := range archive.File {
		name := strings.TrimSpace(entry.Name)
		if name == "" || strings.Contains(name, "\\") {
			return fmt.Errorf("GitHub archive contains an invalid path %q", entry.Name)
		}
		cleaned := pathpkg.Clean(name)
		if cleaned == "." || pathpkg.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return fmt.Errorf("GitHub archive path %q escapes its root", entry.Name)
		}
		parts := strings.Split(cleaned, "/")
		if rootPrefix == "" {
			rootPrefix = parts[0]
		} else if parts[0] != rootPrefix {
			return errors.New("GitHub archive contains multiple top-level roots")
		}
		if len(parts) < 2 {
			continue
		}
		relative := strings.Join(parts[1:], "/")
		if relative == "" || relative == "." {
			continue
		}
		if strings.HasPrefix(relative, "/") || relative == ".." || strings.HasPrefix(relative, "../") || strings.Contains(relative, "/../") {
			return fmt.Errorf("GitHub archive path %q escapes its root", entry.Name)
		}
		key := strings.ToLower(relative)
		if seen[key] {
			return fmt.Errorf("GitHub archive contains duplicate path %q", relative)
		}
		seen[key] = true
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || (!entry.FileInfo().IsDir() && !mode.IsRegular()) {
			return fmt.Errorf("GitHub archive path %q is not a regular file or directory", relative)
		}
		target := filepath.Join(destination, filepath.FromSlash(relative))
		if !pathWithinRoot(target, destination) {
			return fmt.Errorf("GitHub archive path %q escapes its root", relative)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if entry.UncompressedSize64 > uint64(maxMarketplaceExtractedBytes) || total+int64(entry.UncompressedSize64) > maxMarketplaceExtractedBytes {
			return errors.New("GitHub archive exceeds the 100MB extraction limit")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		destinationFile, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			_ = source.Close()
			return err
		}
		written, copyErr := io.Copy(destinationFile, io.LimitReader(source, maxMarketplaceExtractedBytes-total+1))
		closeErr := errors.Join(destinationFile.Close(), source.Close())
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		if written != int64(entry.UncompressedSize64) {
			return fmt.Errorf("GitHub archive entry %q changed size", relative)
		}
		total += written
	}
	return nil
}

func (s *Server) loadWebSkillMarketplaceSources(userID string) ([]webSkillMarketplaceSource, error) {
	if s.settingsStore == nil {
		return []webSkillMarketplaceSource{}, errors.New("settings store is not configured")
	}
	setting, found, err := s.settingsStore.Get(webSkillUserSettingKey(webSkillMarketplaceSourcesSetting, userID))
	if err != nil || !found {
		return []webSkillMarketplaceSource{}, err
	}
	raw, err := json.Marshal(setting.Value)
	if err != nil {
		return nil, err
	}
	sources := []webSkillMarketplaceSource{}
	if err := json.Unmarshal(raw, &sources); err != nil {
		return nil, err
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Slug < sources[j].Slug })
	return sources, nil
}

func (s *Server) saveWebSkillMarketplaceSources(userID string, sources []webSkillMarketplaceSource) error {
	_, err := s.settingsStore.Set(webSkillUserSettingKey(webSkillMarketplaceSourcesSetting, userID), sources)
	return err
}

func (s *Server) removeWebSkillMarketplaceSource(userID, slug string) error {
	if s == nil || s.workspaceStore == nil {
		return errors.New("workspace store is not configured")
	}
	// Read provenance and verify the actual directories only after joining
	// the same publication lock as installers. Pre-lock hashes can describe
	// a directory that another import has already replaced.
	s.skillMutationMu.Lock()
	defer s.skillMutationMu.Unlock()
	filesystem, err := openSkillImportFilesystem(s.fileRoot, false)
	if err != nil {
		return err
	}
	defer filesystem.close()
	sources, err := s.loadWebSkillMarketplaceSources(userID)
	if err != nil {
		return err
	}
	var selected webSkillMarketplaceSource
	found := false
	next := make([]webSkillMarketplaceSource, 0, len(sources))
	for _, source := range sources {
		if source.Slug == slug {
			selected = source
			found = true
			continue
		}
		next = append(next, source)
	}
	if !found {
		return fmt.Errorf("%w: marketplace source %s not found", os.ErrNotExist, slug)
	}
	agents, err := s.workspaceStore.ListAgents(userID)
	if err != nil {
		return err
	}
	for _, name := range selected.Skills {
		root := filepath.Join(filesystem.path, name)
		directory, openErr := filesystem.openDirectory(root)
		if openErr != nil {
			return openErr
		}
		hash, hashErr := webSkillDirectoryHash(directory)
		_ = directory.Close()
		if hashErr != nil || hash != selected.Hashes[name] {
			return fmt.Errorf("skill %s changed after import; remove it manually after reviewing local edits", name)
		}
		for _, agent := range agents {
			for _, attached := range agent.SkillNames {
				if strings.EqualFold(attached, name) {
					return fmt.Errorf("skill %s is still attached to agent %s", name, agent.Name)
				}
			}
		}
	}

	quarantine, quarantineOwner, err := filesystem.createPrivateDirectory()
	if err != nil {
		return err
	}
	journal := make([]*skillDirectoryPublication, 0, len(selected.Skills))
	rollback := func(cause error) error {
		restoreErr := rollbackSkillPublications(journal)
		if restoreErr == nil {
			current, err := filesystem.lstat(quarantine)
			if err != nil || !os.SameFile(current, quarantineOwner) {
				restoreErr = errors.New("marketplace removal recovery retained an unverified quarantine owner")
			} else {
				_ = filesystem.remove(quarantine, true)
			}
		}
		return errors.Join(cause, restoreErr)
	}
	for _, name := range selected.Skills {
		source := filepath.Join(filesystem.path, name)
		target := filepath.Join(quarantine, name)
		identity, err := filesystem.lstat(source)
		if err != nil {
			return rollback(err)
		}
		if err := filesystem.rename(source, target); err != nil {
			return rollback(err)
		}
		journal = append(journal, &skillDirectoryPublication{
			filesystem: filesystem, source: source, destination: target, published: identity,
		})
	}
	if err := s.saveWebSkillMarketplaceSources(userID, next); err != nil {
		return rollback(err)
	}
	for _, name := range selected.Skills {
		s.skillCatalog.RemoveSkill(name)
	}
	current, err := filesystem.lstat(quarantine)
	if err != nil || !os.SameFile(current, quarantineOwner) {
		return errors.New("marketplace source was removed but private quarantine ownership changed")
	}
	if err := filesystem.remove(quarantine, true); err != nil {
		return fmt.Errorf("marketplace source was removed but quarantine cleanup failed: %w", err)
	}
	return nil
}

func normalizeMarketplaceLicense(value *string) *string {
	if value == nil {
		return nil
	}
	normalized := strings.TrimSpace(*value)
	if normalized == "" || len(normalized) > 128 {
		return nil
	}
	return &normalized
}
