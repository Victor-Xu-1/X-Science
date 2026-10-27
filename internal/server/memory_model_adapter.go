package server

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryclassifier"
	"x-science/internal/memorypolicy"
	"x-science/internal/memorytools"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/providers"
)

type serverMemoryClassifierModel struct {
	server *Server
	scope  memorytools.Scope
	model  string
}

type memoryRuntimeModelClient struct {
	client agentruntime.ModelClient
}

type memoryExtractionModelContextKey struct{}

func (client memoryRuntimeModelClient) Complete(ctx context.Context, request agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	if client.client == nil {
		return agentruntime.ModelResponse{}, errors.New("memory model client is unavailable")
	}
	if choice, ok := request.ToolChoice.(map[string]any); ok && strings.EqualFold(strings.TrimSpace(stringValue(choice["type"])), "none") {
		request.ToolChoice = "none"
	}
	return client.client.Complete(ctx, request)
}

func (s *Server) newMemoryClassifier(scope memorytools.Scope) *memoryclassifier.Gate {
	return s.newMemoryClassifierForModel(scope, "")
}

func (s *Server) newMemoryClassifierForModel(scope memorytools.Scope, sessionModel string) *memoryclassifier.Gate {
	model := strings.TrimSpace(sessionModel)
	return memoryclassifier.New(&serverMemoryClassifierModel{server: s, scope: scope, model: model}, s.memoryConfig.PIClassifierEnabled)
}

func (m *serverMemoryClassifierModel) ClassifyPromptInjection(ctx context.Context, body string) (memoryclassifier.ModelResponse, error) {
	if m == nil || m.server == nil || m.server.workspaceStore == nil {
		return memoryclassifier.ModelResponse{}, unavailableMemoryClassifier(errors.New("memory classifier runtime is unavailable"))
	}
	prompt, err := memoryclassifier.BuildPromptInjectionPrompt(body)
	if err != nil {
		return memoryclassifier.ModelResponse{}, err
	}
	client, err := m.server.newMemoryRuntimeModelClient(ctx, m.scope, strings.TrimSpace(m.model), memorypolicy.PIClassifierDeadline, memorypolicy.PIClassifierBusyRetries+1)
	if err != nil {
		return memoryclassifier.ModelResponse{}, unavailableMemoryClassifier(err)
	}
	temperature := 1.0
	response, err := client.Complete(ctx, agentruntime.ModelRequest{
		Messages:  []agentruntime.Message{{Role: "system", Content: prompt.System}, {Role: "user", Content: prompt.User}},
		MaxTokens: memorypolicy.PIClassifierMaxTokens, Temperature: &temperature,
	})
	if err != nil {
		if isMemoryClassifierUnavailableError(err) {
			return memoryclassifier.ModelResponse{}, unavailableMemoryClassifier(err)
		}
		return memoryclassifier.ModelResponse{}, err
	}
	return memoryclassifier.ModelResponse{Text: response.Message.Content, StopReason: response.StopReason}, nil
}

func (s *Server) newMemoryRuntimeModelClient(ctx context.Context, scope memorytools.Scope, model string, timeout time.Duration, maxAttempts int) (agentruntime.ModelClient, error) {
	if s == nil || s.workspaceStore == nil {
		return nil, errors.New("memory model runtime is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	input := providers.ResolutionInput{
		Context: ctx, ProjectID: strings.TrimSpace(scope.ProjectID),
		RequestTimeout: timeout, MaxAttempts: maxAttempts,
	}
	userID := strings.TrimSpace(scope.UserID)
	var profile providers.ModelProfile
	var err error
	if pinned, ok := ctx.Value(memoryExtractionModelContextKey{}).(workspace.MemoryExtractionModelIdentity); ok {
		if pinned.Model == "" {
			return nil, errors.New("original Memory model identity is unavailable")
		}
		if pinned.Selection != "" {
			profile, err = s.resolveUserModelSelectionProfileWithContext(ctx, userID, pinned.Selection, input)
		} else {
			profile, err = providers.ResolveUserModelProfile(s.settingsStore, s.workspaceStore, s.secretStore, userID, pinned.Model, input)
		}
		if err != nil {
			return nil, err
		}
		identity := strings.Join([]string{strings.TrimSpace(profile.Provider.ID), strings.TrimSpace(profile.Provider.Endpoint), strings.TrimSpace(profile.Model)}, "\x00")
		if profile.Model != pinned.Model || pinned.ProviderID != "" && profile.Provider.ID != pinned.ProviderID || pinned.ProfileSHA256 != "" && memoryViewDigest([]byte(identity)) != pinned.ProfileSHA256 {
			return nil, errors.New("original Memory model profile changed or is unavailable")
		}
		client, err := providers.NewRuntimeModelClient(profile, s.httpClient, nil)
		if err != nil {
			return nil, err
		}
		return memoryRuntimeModelClient{client: client}, nil
	}
	snapshot := sessionConversationModelSnapshot{}
	if sourceFrameID := strings.TrimSpace(scope.SourceFrameID); sourceFrameID != "" {
		snapshot, err = s.sessionConversationModelSnapshotWithContext(ctx, sourceFrameID)
		if err != nil {
			return nil, err
		}
	}
	if snapshot.Selection != "" {
		if snapshot.OwnerUserID == "" || snapshot.OwnerUserID != userID {
			return nil, errors.New("memory model owner does not match the conversation")
		}
		profile, err = s.resolveUserModelSelectionProfileWithContext(ctx, userID, snapshot.Selection, input)
	} else {
		profile, err = providers.ResolveUserModelProfile(
			s.settingsStore, s.workspaceStore, s.secretStore, userID, strings.TrimSpace(model), input,
		)
	}
	if err != nil {
		return nil, err
	}
	client, err := providers.NewRuntimeModelClient(profile, s.httpClient, nil)
	if err != nil {
		return nil, err
	}
	return memoryRuntimeModelClient{client: client}, nil
}

func unavailableMemoryClassifier(err error) error {
	return &memoryclassifier.UnavailableError{Cause: err}
}

func isMemoryClassifierUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return true
	}
	var urlError *url.Error
	if errors.As(err, &urlError) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, fragment := range []string{"provider endpoint returned ", "rate limit", "usage limit", "connection", "authentication", "unauthorized", "forbidden", "model not found"} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}
