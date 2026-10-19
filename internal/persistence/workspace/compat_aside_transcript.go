package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/sessionagent"
)

// CreateCompatibilityAsideWithTranscript creates the child Frame, immutable
// history references, and first runnable input in one SQLite transaction.
func (s *Store) CreateCompatibilityAsideWithTranscript(
	ctx context.Context,
	input CompatibilityAsideInput,
	messageID string,
	destinations []string,
) (CompatibilityAsideResult, error) {
	if s == nil || s.db == nil {
		return CompatibilityAsideResult{}, errors.New("workspace store is closed")
	}
	input, err := normalizeCompatibilityAsideInput(input)
	if err != nil {
		return CompatibilityAsideResult{}, err
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return CompatibilityAsideResult{}, errors.New("aside message id is required")
	}

	s.branchMu.Lock()
	defer s.branchMu.Unlock()
	repository := transcriptstore.NewRepository(s.db)
	var result CompatibilityAsideResult
	err = repository.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		ownerID, err := frameOwnerInTransaction(ctx, tx, input.ParentRootFrameID)
		if err != nil {
			return err
		}
		if replay, found, err := lookupCompatibilityAsideIntent(ctx, tx, ownerID, input.ParentRootFrameID, input.IntentID, &input); err != nil {
			return err
		} else if found {
			result = replay
			return nil
		}
		var parentStreamUID string
		if err := tx.QueryRowContext(ctx, `
			SELECT authority.active_stream_uid FROM transcript_frame_authority authority
			JOIN transcript_streams stream
				ON stream.stream_uid=authority.active_stream_uid AND stream.epoch=authority.active_epoch
			WHERE authority.owner_id=? AND authority.session_id=?
				AND authority.read_authority='transcript_payload_v1'
				AND authority.write_authority='transcript_payload_v1'
				AND stream.owner_id=authority.owner_id AND stream.frame_id=? AND stream.kind='frame_ref'`,
			ownerID, input.ParentRootFrameID, input.ParentRootFrameID).Scan(&parentStreamUID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrCompatibilityAsideParentTranscriptMissing
			}
			return err
		}
		selection, err := tx.ResolveAsideHistorySelection(ctx, parentStreamUID, ownerID, input.SourceBranchID, input.ExpectedGeneration)
		if err != nil {
			return err
		}
		var parentIncarnation string
		if err := tx.QueryRowContext(ctx, `SELECT incarnation_id FROM frames WHERE id=?`, input.ParentRootFrameID).Scan(&parentIncarnation); err != nil {
			return err
		}
		if parentIncarnation == "" {
			return ErrCompatibilityAsideAcceptanceUnavailable
		}
		input.canonicalAcceptance = &CompatibilityAsideAcceptance{
			OwnerID: ownerID, ParentRootFrameID: input.ParentRootFrameID, ParentIncarnationID: parentIncarnation, IntentID: input.IntentID,
			FrameID: input.ID, InputMessageID: messageID, SourceBranchID: selection.BranchID,
			SourceGeneration: selection.Generation, ThroughPublication: selection.ThroughPublication, AsSession: input.AsSession,
		}
		parentConfig, found, err := tx.LatestFrameRuntimeConfig(ctx, parentStreamUID, ownerID, selection)
		if err != nil {
			return err
		}
		if !found {
			parentConfig = map[string]any{}
		}
		input.RuntimeConfig = parentConfig
		definition, err := resolveCompatibilityAsideSpecialist(ctx, tx, ownerID, &input)
		if err != nil {
			return err
		}
		stripThinking := input.Model != nil && !compatibilityAsideEqualScalar(*input.Model, parentConfig["model"])
		if definition != nil && input.Model == nil {
			stripThinking = !compatibilityAsideEqualScalar(input.RuntimeConfig["model"], parentConfig["model"])
		}
		input.canonicalSeedCount, err = tx.CountAsideHistory(ctx, parentStreamUID, ownerID, stripThinking, selection)
		if err != nil {
			return err
		}
		input.canonicalSeed = true
		result, err = s.createCompatibilityAsideInTransaction(ctx, tx, input)
		if err != nil {
			return err
		}
		stream, err := tx.CreateStream(ctx, transcriptstore.CreateStreamInput{
			UID: "frame:" + result.Frame.ID, OwnerID: ownerID, ExternalID: result.Frame.ID,
			SessionID: result.Frame.ID, Kind: transcriptstore.StreamKindFrameRef,
			ProjectID: result.Frame.ProjectID, RootFrameID: result.Frame.RootFrameID,
			FrameID: result.Frame.ID, Epoch: 1,
		})
		if err != nil {
			return err
		}
		if _, err := tx.CloneAsideHistory(ctx, transcriptstore.CloneAsideHistoryInput{
			SourceStreamUID: parentStreamUID, TargetStreamUID: stream.UID,
			OwnerID: ownerID, IncludeForkNotice: input.AsSession, StripThinking: stripThinking,
			Selection: selection,
		}); err != nil {
			return err
		}
		if err := prepareCompatibilityAsideSpecialist(ctx, tx, ownerID, input, &result, definition); err != nil {
			return err
		}
		origin := ""
		var inputData map[string]any
		if definition != nil {
			origin, inputData = "task_intent", result.InputData
			if result.Acceptance.SpecialistBinding == nil || result.InputData[sessionagent.InputReferenceKey] == nil {
				return ErrCompatibilityAsideAcceptanceUnavailable
			}
		}
		_, _, created, err := tx.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
			StreamUID: stream.UID, OwnerID: ownerID, ClientMessageID: messageID,
			FrameEventID: "frame-message:" + result.Frame.ID + ":" + messageID,
			MessageUUID:  messageID, Text: input.Request,
			RuntimeConfig: compatibilityAsideTranscriptRuntimeConfig(result), Destinations: destinations,
			MessageOrigin: origin, InputData: inputData,
		})
		if err != nil {
			return err
		}
		if !created {
			return transcriptstore.ErrEventConflict
		}
		return writeCompatibilityAsideSpecialistProof(ctx, tx, ownerID, input, result)
	})
	if err != nil {
		return CompatibilityAsideResult{}, err
	}
	return result, nil
}

func compatibilityAsideTranscriptRuntimeConfig(result CompatibilityAsideResult) map[string]any {
	config := map[string]any{"agentName": result.Frame.AgentName}
	if result.Model != nil {
		config["model"] = result.Model
	}
	if result.Effort != nil {
		config["effort"] = result.Effort
	}
	for _, key := range compatibilityAsideSessionKnobs {
		if value, found := result.InputData[key]; found {
			config[key] = value
		}
	}
	return config
}
