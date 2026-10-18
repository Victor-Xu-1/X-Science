package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"x-science/internal/skills"
)

type skillMarketplaceImportInput struct {
	Repo    string   `json:"repo"`
	SHA     string   `json:"sha"`
	Skills  []string `json:"skills"`
	License *string  `json:"license"`
}

type skillMarketplaceImportResult struct {
	imported []string
	skipped  []map[string]any
	source   *webSkillMarketplaceSource
}

func (result skillMarketplaceImportResult) projection() map[string]any {
	output := map[string]any{"imported": result.imported, "skipped": result.skipped, "slug": nil, "sha": nil}
	if result.source != nil {
		output["slug"], output["sha"] = result.source.Slug, result.source.SHA
	}
	return output
}

type skillMarketplaceImportError struct {
	status int
	cause  error
}

func (err *skillMarketplaceImportError) Error() string { return err.cause.Error() }
func (err *skillMarketplaceImportError) Unwrap() error { return err.cause }

func marketplaceImportFailure(status int, err error) error {
	return &skillMarketplaceImportError{status: status, cause: err}
}

// Import authority is supplied only by a Go caller. HTTP operators use the
// immediate commit; a kernel must carry its exact bound execution through the
// same core to publication. Request JSON cannot select or manufacture a guard.
func (s *Server) importMarketplaceSkills(
	ctx context.Context, userID string, input skillMarketplaceImportInput, commit skillDraftCommit,
) (skillMarketplaceImportResult, error) {
	result := skillMarketplaceImportResult{imported: []string{}, skipped: []map[string]any{}}
	if commit == nil {
		return result, errors.New("marketplace import commit authority is required")
	}
	repository, err := parseGitHubSkillRepository(input.Repo)
	if err != nil {
		return result, marketplaceImportFailure(http.StatusBadRequest, err)
	}
	input.SHA = strings.ToLower(strings.TrimSpace(input.SHA))
	if !githubCommitPattern.MatchString(input.SHA) {
		return result, marketplaceImportFailure(http.StatusBadRequest, errors.New("sha must be a full 40-character Git commit"))
	}
	selected := normalizedSkillNameSet(input.Skills)
	if len(selected) == 0 || len(selected) > maxMarketplaceSkills {
		return result, marketplaceImportFailure(http.StatusBadRequest, errors.New("between 1 and 100 skills must be selected"))
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	resolved, err := s.resolveGitHubSkillCommit(ctx, repository, input.SHA)
	if err != nil || !strings.EqualFold(resolved, input.SHA) {
		return result, marketplaceImportFailure(http.StatusConflict, errors.New("the selected Git commit could not be verified"))
	}
	verifiedLicense, licenseErr := s.resolveGitHubSkillLicense(ctx, repository)
	if licenseErr != nil {
		verifiedLicense = nil
	}
	items, cleanup, err := s.downloadGitHubSkillRepository(ctx, repository, input.SHA)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return result, marketplaceImportFailure(http.StatusBadGateway, err)
	}
	byName := make(map[string]skills.Skill, len(items))
	for _, skill := range items {
		key := strings.ToLower(skill.Name)
		if _, duplicate := byName[key]; duplicate {
			return result, marketplaceImportFailure(http.StatusBadRequest, errors.New("repository contains duplicate skill names"))
		}
		byName[key] = skill
	}
	for name := range selected {
		if _, found := byName[name]; !found {
			return result, marketplaceImportFailure(http.StatusBadRequest, fmt.Errorf("selected skill is absent from the pinned repository: %s", name))
		}
	}
	prepared := make([]*preparedSkillImport, 0, len(selected))
	defer func() {
		for _, item := range prepared {
			item.cleanup()
		}
	}()
	operationID := uuid.NewString()
	history := make([]webSkillImportHistoryEntry, 0, len(selected))
	addFailure := func(name string, err error) {
		result.skipped = append(result.skipped, map[string]any{"name": name, "reason": err.Error()})
		history = append(history, newWebSkillImportHistory(
			operationID, repository.Canonical(), repository.Canonical(), name, name, "failed", "IMPORT_FAILED",
		))
	}
	for _, skill := range items {
		if _, wanted := selected[strings.ToLower(skill.Name)]; !wanted {
			continue
		}
		raw, err := workspaceSkillArchive(skill)
		var item *preparedSkillImport
		if err == nil {
			item, err = s.prepareSkillBundle(skill.Name+".zip", "", false, raw)
		}
		if err != nil {
			addFailure(skill.Name, err)
			continue
		}
		prepared = append(prepared, item)
	}
	_, _, err = s.publishPreparedSkillImports(prepared, commit, func(imported []skillImportResult, conflicts []skillImportFailure) error {
		for _, conflict := range conflicts {
			addFailure(conflict.name, conflict.err)
		}
		hashes := make(map[string]string, len(imported))
		for _, installed := range imported {
			result.imported = append(result.imported, installed.Name)
			for _, item := range prepared {
				if strings.EqualFold(item.skill.Name, installed.Name) {
					hashes[installed.Name] = item.hash
					break
				}
			}
			history = append(history, newWebSkillImportHistory(
				operationID, repository.Canonical(), repository.Canonical(), installed.Name, installed.Name, "imported", "",
			))
		}
		sort.Strings(result.imported)
		if len(result.imported) > 0 {
			result.source = &webSkillMarketplaceSource{
				Slug: repository.Slug(), Repo: repository.Canonical(), SHA: input.SHA,
				License: normalizeMarketplaceLicense(verifiedLicense), Skills: result.imported,
				ImportedAt: time.Now().UTC().Format(time.RFC3339Nano), Removable: true, Hashes: hashes,
			}
		}
		return s.recordSkillImports(userID, result.source, history)
	})
	if err != nil {
		return result, err
	}
	return result, nil
}
