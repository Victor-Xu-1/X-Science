package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

var (
	ErrExtractionSourceChanged = errors.New("completed memory extraction source changed")
	ErrExtractionPlanConflict  = errors.New("completed memory extraction plan conflicts with its receipt")
	ErrExtractionCursorChanged = errors.New("memory extraction cursor changed during preparation")
)

type extractionSourceIdentity struct {
	source      CompletedExtractionSource
	key         string
	encoded     string
	digest      string
	incarnation string
}

func identifyExtractionSource(source CompletedExtractionSource) (extractionSourceIdentity, error) {
	invalid := func() (extractionSourceIdentity, error) {
		return extractionSourceIdentity{}, ErrExtractionSourceChanged
	}
	if source.Schema != 1 || (source.Transcript == nil) == (source.Legacy == nil) {
		return invalid()
	}
	for _, value := range []string{source.OwnerUserID, source.ProjectID, source.FrameID} {
		if value == "" || value != strings.TrimSpace(value) || len(value) > 512 || strings.ContainsAny(value, "\x00\r\n") {
			return invalid()
		}
	}
	// The logical completion key deliberately excludes mutable branch-generation
	// fields. Their full source binding is compared separately, never treated as
	// permission to apply another plan to the same completed canonical event.
	key := struct {
		Authority, Owner, Project, Frame, Incarnation, Stream, Session, Prefix string
		Epoch, Event, Attempt                                                  int64
	}{Owner: source.OwnerUserID, Project: source.ProjectID, Frame: source.FrameID}
	if source.Transcript != nil {
		proof := *source.Transcript
		source.Transcript = &proof
		if proof.StreamUID == "" || proof.OwnerID != source.OwnerUserID || proof.FrameID != source.FrameID ||
			proof.RootFrameID != source.FrameID || proof.FrameIncarnationID == "" ||
			proof.RootFrameIncarnationID != proof.FrameIncarnationID || proof.StreamEpoch <= 0 ||
			proof.EventID <= 0 || proof.PublicationSeq <= 0 || proof.Attempt <= 0 || proof.InputRevision <= 0 ||
			proof.BranchID == "" || proof.BranchGeneration <= 0 {
			return invalid()
		}
		key.Authority, key.Incarnation, key.Stream = "transcript", proof.FrameIncarnationID, proof.StreamUID
		key.Epoch, key.Event, key.Attempt = proof.StreamEpoch, proof.EventID, proof.Attempt
	} else {
		legacy := *source.Legacy
		legacy.PrefixSHA256 = normalizedExtractionDigest(legacy.PrefixSHA256)
		source.Legacy = &legacy
		if legacy.SessionID == "" || legacy.FrameIncarnationID == "" || legacy.FinishEventID <= 0 || legacy.PrefixSHA256 == "" {
			return invalid()
		}
		// Legacy journals may replace and renumber their contents. Their strict
		// completed prefix is therefore part of the logical history identity.
		key.Authority, key.Incarnation = "legacy", legacy.FrameIncarnationID
		key.Session, key.Prefix, key.Event = legacy.SessionID, legacy.PrefixSHA256, legacy.FinishEventID
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		return invalid()
	}
	keyJSON, err := json.Marshal(key)
	if err != nil {
		return invalid()
	}
	return extractionSourceIdentity{
		source: source, key: extractionDigest(keyJSON), encoded: string(encoded),
		digest: extractionDigest(encoded), incarnation: key.Incarnation,
	}, nil
}

func extractionDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func normalizedExtractionDigest(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return ""
	}
	return value
}

// Receipt reads require the original live owner/root incarnation, but not a
// fresh write grant or the still-selected branch. Committed project/profile
// facts do not become uncommitted when a user switches branches or policy.
func requireExtractionFrameTx(ctx context.Context, tx memoryTransaction, identity extractionSourceIdentity) error {
	var owner, project, root, incarnation, parent string
	err := tx.QueryRowContext(ctx, `SELECT project.user_id,frame.project_id,frame.root_frame_id,frame.incarnation_id,
		COALESCE(frame.parent_frame_id,'') FROM frames frame JOIN projects project ON project.id=frame.project_id
		WHERE frame.id=?`, identity.source.FrameID).Scan(&owner, &project, &root, &incarnation, &parent)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrExtractionSourceChanged
	}
	if err != nil {
		return fmt.Errorf("read memory extraction root: %w", err)
	}
	if owner != identity.source.OwnerUserID || project != identity.source.ProjectID || root != identity.source.FrameID ||
		incarnation != identity.incarnation || parent != "" {
		return ErrExtractionSourceChanged
	}
	return nil
}

func requireFreshExtractionSourceTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, identity extractionSourceIdentity) error {
	if err := requireExtractionFrameTx(ctx, tx, identity); err != nil {
		return err
	}
	if proof := identity.source.Transcript; proof != nil {
		actual, err := tx.ReadCompletedMemorySource(ctx, proof.StreamUID, proof.OwnerID, proof.EventID)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrExtractionSourceChanged, err)
		}
		if actual != *proof {
			return ErrExtractionSourceChanged
		}
	}
	// A legacy caller holds the strict journal guard through this transaction;
	// SQLite owns its frame/incarnation, policy and all resulting side effects.
	return requireExtractionPolicyTx(ctx, tx, identity.source)
}

func requireExtractionPolicyTx(ctx context.Context, tx memoryTransaction, source CompletedExtractionSource) error {
	var userEnabled, projectEnabled sql.NullBool
	var rawInput, rawContext string
	err := tx.QueryRowContext(ctx, `SELECT settings.enabled,project.memory_enabled,
		COALESCE(metadata.input_data,'{}'),COALESCE(metadata.context_data,'{}')
		FROM projects project JOIN frames frame ON frame.project_id=project.id
		LEFT JOIN memory_user_settings settings ON settings.user_id=project.user_id
		LEFT JOIN frame_runtime_metadata metadata ON metadata.frame_id=frame.id
		WHERE project.id=? AND project.user_id=? AND frame.id=?`, source.ProjectID, source.OwnerUserID, source.FrameID).
		Scan(&userEnabled, &projectEnabled, &rawInput, &rawContext)
	if err != nil {
		return fmt.Errorf("read memory extraction policy: %w", err)
	}
	if userEnabled.Valid && !userEnabled.Bool || projectEnabled.Valid && !projectEnabled.Bool {
		return fmt.Errorf("%w: extraction is disabled", ErrAgentMemoryMutationForbidden)
	}
	var input, contextData map[string]json.RawMessage
	if json.Unmarshal([]byte(rawInput), &input) != nil || json.Unmarshal([]byte(rawContext), &contextData) != nil {
		return fmt.Errorf("%w: extraction metadata is invalid", ErrExtractionSourceChanged)
	}
	for _, values := range []map[string]json.RawMessage{input, contextData} {
		if raw, found := values["_aside_parent"]; found && string(raw) != "null" {
			return fmt.Errorf("%w: aside memory extraction is disabled", ErrAgentMemoryMutationForbidden)
		}
	}
	var original map[string]json.RawMessage
	if raw, found := contextData["_original_input"]; found {
		// Match the existing frame preference projection: only a map is an
		// original input, and an existing key wins even when its value is not a
		// string. Do not reinterpret a lower-priority stale input preference.
		_ = json.Unmarshal(raw, &original)
	}
	mode, found := extractionMemoryModeValue(original)
	if !found {
		mode, _ = extractionMemoryModeValue(input)
	}
	if mode == "off" {
		return fmt.Errorf("%w: session memory extraction is disabled", ErrAgentMemoryMutationForbidden)
	}
	return nil
}

func extractionMemoryModeValue(values map[string]json.RawMessage) (string, bool) {
	for _, key := range []string{"memory_mode", "memoryMode"} {
		if raw, found := values[key]; found {
			var mode string
			_ = json.Unmarshal(raw, &mode)
			return mode, true
		}
	}
	return "", false
}
