package server

import (
	"context"
	"net/http"

	workspace "x-science/internal/persistence/workspace"
)

func workspaceMutationContext(r *http.Request) context.Context {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = r.Header.Get("X-Science-Idempotency-Key")
	}
	return workspace.WithMutationIdempotencyKey(r.Context(), key)
}
