package server

import (
	"context"
	"encoding/json"
	"errors"

	"x-science/internal/kernelcontract"
)

// kernelCheckpointAdmissionGateway uses the same task-scoped normalization
// authority as execution. Production callers pass their captured gateway; the
// direct checkpoint entry point resolves the same canonical tool snapshot and
// task state, rather than maintaining a second set of argument transforms.
func (s *Server) kernelCheckpointAdmissionGateway(
	parent context.Context,
	options SessionRunnerChatOptions,
	run *sessionRunnerChatRun,
	name string,
	provided []serverAgentRuntimeToolGateway,
) (serverAgentRuntimeToolGateway, error) {
	if len(provided) > 1 || s == nil || run == nil {
		return serverAgentRuntimeToolGateway{}, errors.New("kernel checkpoint normalization authority is unavailable")
	}
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return serverAgentRuntimeToolGateway{}, err
	}
	if len(provided) == 1 {
		gateway := provided[0]
		if gateway.server != s || gateway.taskRun != run {
			return serverAgentRuntimeToolGateway{}, errors.New("kernel checkpoint normalization belongs to a different task")
		}
		return gateway, nil
	}
	ctx, cancel := context.WithTimeout(parent, sessionRunnerContentDeltaPersistenceTimeout)
	defer cancel()
	ctx = withTranscriptRunnerChatRun(ctx, run)
	if err := ctx.Err(); err != nil {
		return serverAgentRuntimeToolGateway{}, err
	}
	if err := s.hydrateSessionRunnerManagedEnvironmentBindings(ctx, run); err != nil {
		return serverAgentRuntimeToolGateway{}, err
	}
	sessionID := run.SessionID
	if sessionID == "" {
		sessionID = options.SessionID
	}
	schemas := s.agentRuntimeToolSchemasWithContext(ctx, []string{name}, sessionID)
	if err := ctx.Err(); err != nil {
		return serverAgentRuntimeToolGateway{}, err
	}
	kernel := s.resolveAgentKernelContext(ctx, sessionID)
	if err := ctx.Err(); err != nil {
		return serverAgentRuntimeToolGateway{}, err
	}
	return serverAgentRuntimeToolGateway{
		server: s, taskRun: run, sessionID: sessionID,
		kernel:      kernel,
		toolSchemas: schemas, toolValidators: agentRuntimeToolValidators(schemas),
	}, nil
}

func normalizedKernelCheckpointArguments(
	gateway serverAgentRuntimeToolGateway,
	name string,
	raw json.RawMessage,
) (json.RawMessage, error) {
	if !kernelcontract.IsTool(name) || len(raw) == 0 {
		return raw, nil
	}
	// Decoding must not turn duplicate keys or trailing data into a newly
	// executable proposal; the durable kernel contract rejects both shapes.
	if err := kernelcontract.ValidateJSON(raw); err != nil {
		return nil, err
	}
	var input map[string]any
	if json.Unmarshal(raw, &input) != nil || input == nil {
		return raw, nil // Invalid proposals remain ordinary rejected tool items.
	}
	encoded, err := json.Marshal(gateway.normalizeAdmittedToolArguments(name, input))
	if err != nil {
		return nil, err
	}
	return encoded, nil
}
