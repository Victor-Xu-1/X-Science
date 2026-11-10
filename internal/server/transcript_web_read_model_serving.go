package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

type activatedTranscriptWebReadModel struct {
	stream                   transcriptstore.Stream
	snapshot                 transcriptstore.ProjectionSnapshot
	fence                    transcriptstore.TranscriptWebProjectionFence
	allowQuarantinedSnapshot bool
	allowConsistentPrefix    bool
}

func (s *Server) activatedTranscriptWebReadModel(
	ctx context.Context,
	ownerID, sessionID, requestedBranchID string,
) (activatedTranscriptWebReadModel, bool, error) {
	return s.activatedTranscriptWebReadModelWithCatchup(ctx, ownerID, sessionID, requestedBranchID, true)
}

// Metadata decorations may inspect the same authority and fence without
// starting or joining the history rebuild required by message-serving callers.
func (s *Server) activatedTranscriptWebReadModelWithCatchup(
	ctx context.Context,
	ownerID, sessionID, requestedBranchID string,
	allowCatchup bool,
) (activatedTranscriptWebReadModel, bool, error) {
	return s.activatedTranscriptWebReadModelWithPolicy(ctx, ownerID, sessionID, requestedBranchID, allowCatchup, false)
}

func (s *Server) activatedTranscriptWebReadModelWithPolicy(
	ctx context.Context, ownerID, sessionID, requestedBranchID string,
	allowCatchup, allowConsistentPrefix bool,
) (activatedTranscriptWebReadModel, bool, error) {
	noteTranscriptWebReadStage(ctx, webReadFence)
	if s == nil || s.transcriptStore == nil || s.transcriptWebReadModel == nil {
		return activatedTranscriptWebReadModel{}, false, nil
	}
	if s.transcriptContractErr != nil {
		return activatedTranscriptWebReadModel{}, false, transcriptWebStorageError(s.transcriptContractErr)
	}
	authority, found, err := s.transcriptStore.GetFrameAuthorityBySession(ctx, ownerID, sessionID)
	if err != nil || !found {
		return activatedTranscriptWebReadModel{}, false, transcriptWebStorageError(err)
	}
	if !authority.TranscriptPayloadActive() {
		return activatedTranscriptWebReadModel{}, false, nil
	}
	stream, found, err := s.transcriptStore.GetFrameStreamBySession(ctx, ownerID, sessionID)
	if err != nil || !found {
		return activatedTranscriptWebReadModel{}, true, transcriptWebStorageError(err)
	}
	if stream.UID != authority.ActiveStreamUID || stream.Epoch != authority.ActiveEpoch {
		return activatedTranscriptWebReadModel{}, true, transcriptWebStorageError(transcriptstore.ErrEventConflict)
	}
	snapshot, err := s.transcriptStore.GetProjectionSnapshot(ctx, stream.UID, ownerID)
	if err != nil {
		return activatedTranscriptWebReadModel{}, true, transcriptWebStorageError(err)
	}
	requestedBranchID = strings.TrimSpace(requestedBranchID)
	if requestedBranchID != "" && requestedBranchID != snapshot.BranchID {
		// The optimized read model intentionally tracks only the active branch.
		// A valid non-active branch is still readable through the canonical
		// Transcript branch path in the caller. Validate that coordinate before
		// declining the cache so malformed and missing branches retain the
		// established branch-changed response instead of leaking through as an
		// internal projection error.
		if !validWebTranscriptBranchID(requestedBranchID) {
			return activatedTranscriptWebReadModel{}, true, transcriptWebStorageError(transcriptstore.ErrBranchStateStale)
		}
		if _, branchErr := s.transcriptStore.GetBranchProjectionSnapshot(
			ctx, stream.UID, ownerID, requestedBranchID,
		); branchErr != nil {
			if errors.Is(branchErr, transcriptstore.ErrBranchTargetNotFound) {
				return activatedTranscriptWebReadModel{}, true, transcriptWebStorageError(transcriptstore.ErrBranchStateStale)
			}
			return activatedTranscriptWebReadModel{}, true, transcriptWebStorageError(branchErr)
		}
		return activatedTranscriptWebReadModel{}, false, nil
	}
	fence, err := s.transcriptWebReadModel.GetTranscriptWebProjectionFence(
		ctx, ownerID, stream.UID, snapshot.BranchID,
	)
	if err != nil {
		return activatedTranscriptWebReadModel{}, true, transcriptWebStorageError(err)
	}
	if allowCatchup && transcriptWebFenceCanCatchUp(fence) && !(allowConsistentPrefix && fence.ConsistentPrefixEligible()) {
		if catchupErr := s.catchUpTranscriptWebReadModel(
			ctx, ownerID, stream.UID, snapshot.BranchID,
		); catchupErr == nil || errors.Is(catchupErr, transcriptstore.ErrBranchStateStale) ||
			errors.Is(catchupErr, transcriptstore.ErrTranscriptWebProjectionStale) {
			noteTranscriptWebReadStage(ctx, webReadFence)
			fence, err = s.transcriptWebReadModel.GetTranscriptWebProjectionFence(
				ctx, ownerID, stream.UID, snapshot.BranchID,
			)
			if err != nil {
				return activatedTranscriptWebReadModel{}, true, transcriptWebStorageError(err)
			}
		}
	}
	noteTranscriptWebReadStage(ctx, webReadFence)
	allowQuarantinedSnapshot := false
	servingThrough := fence.ThroughPublicationSequence
	if allowConsistentPrefix {
		if fence.ConsistentPrefixEligible() {
			servingThrough = fence.StateThroughPublicationSequence
			// The existing delivery/projector coordinator remains the only worker.
			// Wake it without waiting on the flight owned by another reader.
			s.signalTranscriptWebDelivery()
		} else if !transcriptWebFenceReady(fence) || fence.SourceDirty {
			return activatedTranscriptWebReadModel{}, true, transcriptWebAuthorityUnavailableError()
		}
	} else if !transcriptWebFenceReady(fence) {
		if !transcriptWebVerifiedSnapshotEligible(fence) {
			return activatedTranscriptWebReadModel{}, true, transcriptWebAuthorityUnavailableError()
		}
		allowQuarantinedSnapshot = true
		fence.BranchGeneration = fence.StateBranchGeneration
		fence.ThroughPublicationSequence = fence.StateThroughPublicationSequence
		fence.SourceRevision = fence.StateSourceRevision
		servingThrough = fence.StateThroughPublicationSequence
	}
	return activatedTranscriptWebReadModel{
		stream: stream,
		snapshot: transcriptstore.ProjectionSnapshot{
			StreamUID: stream.UID, BranchID: fence.BranchID,
			BranchGeneration:           fence.BranchGeneration,
			ThroughPublicationSequence: servingThrough,
		},
		fence: fence, allowQuarantinedSnapshot: allowQuarantinedSnapshot, allowConsistentPrefix: allowConsistentPrefix,
	}, true, nil
}

func transcriptWebVerifiedSnapshotEligible(fence transcriptstore.TranscriptWebProjectionFence) bool {
	return fence.StateFound && fence.StateStatus == "quarantined" &&
		fence.StateProjectorVersion == transcriptstore.TranscriptWebProjectorVersion &&
		fence.StateMessageCount > 0 && fence.VisibleMessageCount > 0 &&
		fence.StateBranchGeneration == fence.BranchGeneration &&
		fence.StateThroughPublicationSequence <= fence.ThroughPublicationSequence &&
		fence.StateSourceRevision <= fence.SourceRevision
}

func validWebTranscriptBranchID(value string) bool {
	if len(value) != 11 || !strings.HasPrefix(value, "br_") {
		return false
	}
	for _, character := range value[3:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func (s *Server) writeActivatedTranscriptWebReadModelPage(
	w http.ResponseWriter,
	r *http.Request,
	frameID string,
	active activatedTranscriptWebReadModel,
	limit int,
	ownerID string,
) {
	noteTranscriptWebReadStage(r.Context(), webReadPage)
	before := strings.TrimSpace(r.URL.Query().Get("before"))
	after := strings.TrimSpace(r.URL.Query().Get("after"))
	anchor := strings.TrimSpace(r.URL.Query().Get("anchor_message_id"))
	if before != "" && after != "" {
		writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"message": "before and after cursors are mutually exclusive"})
		return
	}
	total := active.fence.VisibleMessageCount
	var from *int
	if before != "" {
		cursor, status, err := s.resolveTranscriptWebReadModelCursor(
			r, frameID, before, active, ownerID,
		)
		if err != nil {
			if status == http.StatusServiceUnavailable {
				writeWebConversationError(w, err)
				return
			}
			writeWorkspaceJSON(w, status, map[string]any{"message": err.Error()})
			return
		}
		start := max(0, cursor.Index-limit)
		from = &start
	} else if after != "" {
		cursor, status, err := s.resolveTranscriptWebReadModelCursor(
			r, frameID, after, active, ownerID,
		)
		if err != nil {
			if status == http.StatusServiceUnavailable {
				writeWebConversationError(w, err)
				return
			}
			writeWorkspaceJSON(w, status, map[string]any{"message": err.Error()})
			return
		}
		start := min(total, cursor.Index+1)
		from = &start
	} else if anchor != "" {
		index, found, err := s.transcriptWebReadModel.LocateTranscriptWebMessage(r.Context(),
			active.transcriptWebMessageIdentity(ownerID, anchor),
		)
		if err != nil {
			writeWebConversationError(w, transcriptWebReadModelServingError(err))
			return
		}
		if !found {
			if active.allowConsistentPrefix && active.snapshot.ThroughPublicationSequence < active.fence.ThroughPublicationSequence {
				writeWebConversationError(w, transcriptWebAuthorityUnavailableError())
				return
			}
			writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"message": "anchor message was not found"})
			return
		}
		start := max(0, min(index-limit/2, max(0, total-limit)))
		from = &start
	}
	page, found, err := s.transcriptWebReadModel.GetTranscriptWebMessageRange(
		r.Context(), transcriptstore.TranscriptWebMessagePageInput{
			OwnerID: ownerID, StreamUID: active.stream.UID, BranchID: active.snapshot.BranchID,
			BranchGeneration:           active.snapshot.BranchGeneration,
			ThroughPublicationSequence: active.snapshot.ThroughPublicationSequence,
			SourceRevision:             active.fence.SourceRevision, From: from, Limit: limit,
			AllowQuarantinedSnapshot: active.allowQuarantinedSnapshot,
			AllowConsistentPrefix:    active.allowConsistentPrefix,
		},
	)
	if err != nil {
		writeWebConversationError(w, transcriptWebReadModelServingError(err))
		return
	}
	if !found {
		writeWebConversationError(w, transcriptWebAuthorityUnavailableError())
		return
	}
	messages, err := transcriptWebReadModelMessageMaps(page.Messages)
	if err != nil {
		writeWebConversationError(w, transcriptWebStorageError(err))
		return
	}
	noteTranscriptWebReadStage(r.Context(), webReadSanitize)
	s.sanitizeTranscriptWebTerminalMessages(r.Context(), active.stream, messages)
	s.sanitizeTranscriptWebPublicPage(r.Context(), active.stream, active.snapshot, page.Messages, messages)
	if err := s.enrichTranscriptWebConversationMessages(r.Context(), frameID, active.snapshot.BranchID, messages); err != nil {
		writeWebConversationError(w, err)
		return
	}
	items := compactWebConversationMessagePage(r, messages)
	var oldest any
	var newest any
	if len(items) > 0 {
		var sourceRevision *int64
		if active.allowConsistentPrefix {
			sourceRevision = &page.State.SourceRevision
		}
		oldest = encodeTranscriptBranchMessageCursorWithSource(active.snapshot, page.From, sourceRevision)
		newest = encodeTranscriptBranchMessageCursorWithSource(active.snapshot, page.From+len(items)-1, sourceRevision)
	}
	payload := map[string]any{
		"items": items, "oldest_cursor": oldest, "newest_cursor": newest,
		"has_more_before": page.From > 0, "has_more_after": page.From+len(items) < page.Total,
		"branch_id": active.snapshot.BranchID, "branch_generation": active.snapshot.BranchGeneration,
		"through_publication_sequence": active.snapshot.ThroughPublicationSequence,
	}
	if active.allowConsistentPrefix {
		payload["projection_pending"] = page.ProjectionPending
		payload["canonical_through_publication_sequence"] = page.CanonicalThroughPublicationSequence
		payload["source_revision"] = page.State.SourceRevision
	}
	noteTranscriptWebReadStage(r.Context(), webReadResponse)
	writePrivateRevalidatedWorkspaceJSON(w, r, ownerID, payload)
}

func (active activatedTranscriptWebReadModel) transcriptWebMessageIdentity(
	ownerID, identity string,
) transcriptstore.TranscriptWebMessageIdentityInput {
	input := transcriptstore.TranscriptWebMessageIdentityInput{
		OwnerID: ownerID, StreamUID: active.stream.UID, BranchID: active.snapshot.BranchID,
		BranchGeneration:           active.snapshot.BranchGeneration,
		ThroughPublicationSequence: active.snapshot.ThroughPublicationSequence,
		SourceRevision:             active.fence.SourceRevision, Identity: identity,
		AllowQuarantinedSnapshot: active.allowQuarantinedSnapshot,
		AllowConsistentPrefix:    active.allowConsistentPrefix,
	}
	if active.allowConsistentPrefix {
		through := active.fence.ThroughPublicationSequence
		input.ExpectedCanonicalThrough = &through
	}
	return input
}

func (s *Server) resolveTranscriptWebReadModelCursor(
	r *http.Request,
	frameID, value string,
	active activatedTranscriptWebReadModel,
	ownerID string,
) (transcriptBranchMessageCursor, int, error) {
	var sourceRevision *int64
	if active.allowConsistentPrefix {
		sourceRevision = &active.fence.SourceRevision
	}
	cursor, status, err := parseTranscriptBranchMessageCursorWithSource(
		value, active.snapshot, active.fence.VisibleMessageCount, sourceRevision,
	)
	// Opaque prefix cursors can never be reinterpreted as identities or mapped
	// through an older view after a source/coverage/consistency mismatch.
	if cursor.Version != 0 && (active.allowConsistentPrefix || err != nil && (cursor.Version == 2 || status == http.StatusBadRequest)) {
		return cursor, status, err
	}
	if err != nil {
		index, found, locateErr := s.transcriptWebReadModel.LocateTranscriptWebMessage(
			r.Context(), active.transcriptWebMessageIdentity(ownerID, value),
		)
		if locateErr != nil {
			if active.allowConsistentPrefix && (errors.Is(locateErr, transcriptstore.ErrTranscriptWebProjectionStale) || errors.Is(locateErr, transcriptstore.ErrBranchStateStale)) {
				return transcriptBranchMessageCursor{}, http.StatusServiceUnavailable, transcriptWebAuthorityUnavailableError()
			}
			return transcriptBranchMessageCursor{}, http.StatusInternalServerError, errors.New("unable to resolve conversation cursor")
		}
		if found {
			return transcriptBranchMessageCursor{
				Version: 1, BranchID: active.snapshot.BranchID,
				BranchGeneration:           active.snapshot.BranchGeneration,
				ThroughPublicationSequence: active.snapshot.ThroughPublicationSequence,
				Index:                      index,
			}, http.StatusOK, nil
		}
		if active.allowConsistentPrefix && active.snapshot.ThroughPublicationSequence < active.fence.ThroughPublicationSequence {
			return transcriptBranchMessageCursor{}, http.StatusServiceUnavailable, transcriptWebAuthorityUnavailableError()
		}
	}
	if err == nil || cursor.Version != 1 || cursor.BranchID == "" ||
		cursor.BranchGeneration <= 0 || cursor.ThroughPublicationSequence < 0 || cursor.Index < 0 {
		return cursor, status, err
	}
	translated, found, translateErr := s.transcriptStore.ResolveActivatedLegacyCursor(
		r.Context(), ownerID, frameID, cursor.BranchID, cursor.BranchGeneration,
		cursor.ThroughPublicationSequence, cursor.Index,
	)
	if translateErr != nil {
		return transcriptBranchMessageCursor{}, http.StatusInternalServerError, errors.New("unable to resolve conversation cursor")
	}
	if !found || translated.TargetBranchID != active.snapshot.BranchID {
		return cursor, status, err
	}
	index, found, locateErr := s.transcriptWebReadModel.LocateTranscriptWebMessage(
		r.Context(), active.transcriptWebMessageIdentity(ownerID, translated.StableMessageID),
	)
	if locateErr != nil {
		return transcriptBranchMessageCursor{}, http.StatusInternalServerError, errors.New("unable to resolve conversation cursor")
	}
	if !found {
		return cursor, status, err
	}
	return transcriptBranchMessageCursor{
		Version: 1, BranchID: active.snapshot.BranchID,
		BranchGeneration:           active.snapshot.BranchGeneration,
		ThroughPublicationSequence: active.snapshot.ThroughPublicationSequence,
		Index:                      index,
	}, http.StatusOK, nil
}

func transcriptWebReadModelMessageMaps(
	records []transcriptstore.TranscriptWebMessageRecord,
) ([]map[string]any, error) {
	messages := make([]map[string]any, 0, len(records))
	for _, record := range records {
		var message map[string]any
		if len(record.MessageJSON) == 0 || json.Unmarshal(record.MessageJSON, &message) != nil || message == nil {
			return nil, transcriptstore.ErrEventConflict
		}
		message["artifact_refs"] = transcriptWebReadModelArtifactReferenceMaps(
			transcriptWebPresentationArtifactReferences(record.ArtifactReferences, message),
		)
		messages = append(messages, message)
	}
	return messages, nil
}

// transcriptWebPresentationArtifactReferences collapses byte-identical
// generated files that were saved from more than one workspace path. The
// append-only artifact ledger remains untouched for audit and resume; only the
// user-facing file projection is normalized. Explicit links in the assistant
// message win so the visible card and the link always resolve to the same
// immutable version.
func transcriptWebPresentationArtifactReferences(
	references []transcriptstore.TranscriptWebMessageArtifactReference,
	message map[string]any,
) []transcriptstore.TranscriptWebMessageArtifactReference {
	preferredVersions := map[string]struct{}{}
	if content, ok := message["content"].(map[string]any); ok {
		for _, match := range artifactReferencePattern.FindAllStringSubmatch(webString(content["content"]), -1) {
			if len(match) == 2 && strings.TrimSpace(match[1]) != "" {
				preferredVersions[strings.TrimSpace(match[1])] = struct{}{}
			}
		}
	}
	result := make([]transcriptstore.TranscriptWebMessageArtifactReference, 0, len(references))
	positions := map[string]int{}
	for _, reference := range references {
		filename := strings.TrimSpace(reference.Filename)
		contentType := strings.ToLower(strings.TrimSpace(reference.ContentType))
		checksum := strings.ToLower(strings.TrimSpace(reference.Checksum))
		if reference.Relation != transcriptstore.ArtifactRelationProduced ||
			reference.Availability != transcriptstore.ArtifactAvailable ||
			filename == "" || contentType == "" || checksum == "" {
			result = append(result, reference)
			continue
		}
		identity := filename + "\x00" + contentType + "\x00" + checksum
		if position, found := positions[identity]; found {
			_, candidatePreferred := preferredVersions[strings.TrimSpace(reference.VersionID)]
			_, currentPreferred := preferredVersions[strings.TrimSpace(result[position].VersionID)]
			if candidatePreferred && !currentPreferred {
				result[position] = reference
			}
			continue
		}
		positions[identity] = len(result)
		result = append(result, reference)
	}
	return result
}

func transcriptWebReadModelArtifactReferenceMaps(
	references []transcriptstore.TranscriptWebMessageArtifactReference,
) []map[string]any {
	result := make([]map[string]any, 0, len(references))
	for _, reference := range references {
		value := map[string]any{
			"artifact_id": reference.ArtifactID, "version_id": reference.VersionID,
			"relation": string(reference.Relation), "availability": string(reference.Availability),
			"source_event_id": reference.SourceEventID, "ordinal": reference.SourceReferenceOrdinal,
		}
		if reference.RunnerAttempt != nil {
			value["attempt"] = *reference.RunnerAttempt
		}
		if reference.Filename != "" {
			value["filename"] = reference.Filename
			value["content_type"] = reference.ContentType
			value["size_bytes"] = reference.SizeBytes
			value["checksum"] = reference.Checksum
		}
		result = append(result, value)
	}
	return result
}

func transcriptWebReadModelServingError(err error) error {
	if errors.Is(err, transcriptstore.ErrTranscriptWebProjectionStale) ||
		errors.Is(err, transcriptstore.ErrBranchStateStale) {
		return transcriptWebAuthorityUnavailableError()
	}
	return transcriptWebStorageError(err)
}
