package server

import (
	"context"
	"log"
	"strings"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

const (
	skillCatalogChangedEvent       = "skills.catalog.changed"
	skillCatalogNotificationBudget = 2 * time.Second
)

// Only call after the existing mutation guard has released its SQLite transaction.
// Empty invalidations expose no package, actor, path or per-user preference value.
// This optional write has its own short budget, independent of task computation.
// Notification failure never retries or changes an accepted installation outcome.
func (s *Server) notifySkillCatalogChanged(userID string) {
	owner := strings.TrimSpace(userID)
	if userID != "" && owner == "" {
		// Preserve publishUserEvent's rejection instead of turning an invalid
		// private owner into a global event through input normalization.
		log.Printf("Skill catalog invalidation could not be persisted: realtime event user id is required")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), skillCatalogNotificationBudget)
	defer cancel()
	_, err := s.publishCompatEventContext(ctx, workspace.RealtimeEventInput{
		UserID: owner, Type: skillCatalogChangedEvent, Payload: map[string]any{},
	})
	if err != nil {
		log.Printf("Skill catalog invalidation could not be persisted: %v", err)
	}
}
