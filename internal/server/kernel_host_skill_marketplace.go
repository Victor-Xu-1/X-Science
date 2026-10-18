package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) kernelHostInstallMarketplaceSkill(
	ctx context.Context,
	access workspace.KernelFrameAccess,
	callID, repo string,
	selected []string,
	requestedSHA string,
	commit skillDraftCommit,
) (any, error) {
	userID := strings.TrimSpace(access.UserID)
	approvalInput := map[string]any{
		"repo": repo, "sha": strings.TrimSpace(requestedSHA), "skills": append([]string(nil), selected...),
	}
	metadata := map[string]any{
		"title":       "Install Skills for this task",
		"description": "Install Skills from the reviewed repository, verify them, then continue the current task.",
		"repository":  repo,
		"commit":      strings.TrimSpace(requestedSHA),
		"skills":      append([]string(nil), selected...),
	}
	if err := s.requireKernelCapabilityInstallApproval(
		ctx, access, callID, "host.skills.install", "skill", approvalInput, metadata,
	); err != nil {
		return nil, err
	}
	preview, err := s.kernelHostMarketplacePreview(ctx, userID, repo)
	if err != nil {
		return nil, err
	}
	verifiedRepo := stringValue(preview["repo"])
	sha := strings.TrimSpace(requestedSHA)
	if sha == "" {
		sha = stringValue(preview["sha"])
	}
	if verifiedRepo == "" || sha == "" {
		return nil, kernelruntime.NewHostCallError("invalid_response", "marketplace preview did not return a pinned repository and commit")
	}
	if len(selected) == 0 {
		rawSkills, ok := preview["skills"].([]any)
		if !ok || len(rawSkills) == 0 {
			return nil, kernelruntime.NewHostCallError("not_found", "marketplace repository contains no importable Skills")
		}
		for _, raw := range rawSkills {
			if item, ok := raw.(map[string]any); ok {
				if name := strings.TrimSpace(stringValue(item["name"])); name != "" {
					selected = append(selected, name)
				}
			}
		}
	}
	imported, err := s.importMarketplaceSkills(ctx, userID, skillMarketplaceImportInput{
		Repo: verifiedRepo, SHA: sha, Skills: selected,
	}, commit)
	if err != nil {
		var hostError *kernelruntime.HostCallError
		if errors.As(err, &hostError) {
			return nil, hostError
		}
		return nil, kernelruntime.NewHostCallError("marketplace_failed", err.Error())
	}
	if len(imported.imported) == 0 {
		return nil, kernelruntime.NewHostCallError("marketplace_failed", "No selected marketplace Skills could be installed")
	}
	result := imported.projection()
	result["preview_sha"] = sha
	return result, nil
}

func (s *Server) kernelHostMarketplacePreview(ctx context.Context, userID, repo string) (map[string]any, error) {
	raw, err := json.Marshal(map[string]any{"repo": repo})
	if err != nil {
		return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
	}
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/marketplace/preview", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Science-User-Id", strings.TrimSpace(userID))
	response := httptest.NewRecorder()
	s.handleWebSkillMarketplacePreview(response, request)
	body, readErr := io.ReadAll(response.Result().Body)
	if readErr != nil {
		return nil, kernelruntime.NewHostCallError("storage_error", readErr.Error())
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, kernelruntime.NewHostCallError("invalid_response", "marketplace returned invalid JSON")
	}
	if response.Code < http.StatusOK || response.Code >= http.StatusMultipleChoices {
		detail := stringValue(result["detail"])
		if detail == "" {
			detail = stringValue(result["message"])
		}
		if detail == "" {
			detail = stringValue(result["error"])
		}
		if detail == "" {
			detail = fmt.Sprintf("marketplace request failed with status %d", response.Code)
		}
		return nil, kernelruntime.NewHostCallError("marketplace_failed", detail)
	}
	return result, nil
}
