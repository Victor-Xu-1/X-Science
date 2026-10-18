package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrSessionGrantTargetUnavailable = errors.New("session permission target unavailable")
	ErrSessionGrantScopeStale        = errors.New("session permission scope changed")
	ErrSessionGrantRequestInvalid    = errors.New("invalid session permission request")
	ErrApprovalPolicyGrantStale      = errors.New("approval policy grant changed before admission")
)

type SessionPermissionScope struct {
	FrameID                string `json:"frame_id"`
	FrameIncarnationID     string `json:"frame_incarnation_id"`
	RootFrameID            string `json:"root_frame_id"`
	RootFrameIncarnationID string `json:"root_frame_incarnation_id"`
	ProjectID              string `json:"project_id"`
}

type SessionPermissionExpectedScope struct {
	FrameIncarnationID     string `json:"expected_frame_incarnation_id"`
	RootFrameID            string `json:"expected_root_frame_id"`
	RootFrameIncarnationID string `json:"expected_root_frame_incarnation_id"`
}

func sessionPermissionScopeTx(ctx context.Context, tx workspaceTransaction, owner, frameID string, expected *SessionPermissionExpectedScope) (SessionPermissionScope, error) {
	var scope SessionPermissionScope
	err := tx.QueryRowContext(ctx, `SELECT frame.id,frame.incarnation_id,frame.root_frame_id,root.incarnation_id,frame.project_id
		FROM frames frame JOIN projects project ON project.id=frame.project_id AND project.user_id=?
		JOIN frames root ON root.id=frame.root_frame_id AND root.project_id=frame.project_id WHERE frame.id=?`, owner, frameID).
		Scan(&scope.FrameID, &scope.FrameIncarnationID, &scope.RootFrameID, &scope.RootFrameIncarnationID, &scope.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return scope, ErrSessionGrantTargetUnavailable
	}
	if err != nil {
		return scope, err
	}
	if scope.FrameIncarnationID == "" || scope.RootFrameIncarnationID == "" {
		return scope, ErrSessionGrantScopeStale
	}
	if expected != nil {
		if expected.FrameIncarnationID == "" || expected.RootFrameID == "" || expected.RootFrameIncarnationID == "" {
			return scope, ErrSessionGrantRequestInvalid
		}
		if scope.FrameIncarnationID != expected.FrameIncarnationID || scope.RootFrameID != expected.RootFrameID || scope.RootFrameIncarnationID != expected.RootFrameIncarnationID {
			return scope, ErrSessionGrantScopeStale
		}
	}
	return scope, nil
}

func sessionGrantBindingDigest(owner, project, root, incarnation, key string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{owner, project, root, incarnation, "local_exec", key, "conversation"}, "\x00")))
	return hex.EncodeToString(digest[:])
}

// The existing primary key is an opaque renewal generation and scope binding.
// No legacy row is rewritten merely because it is read.
func newLocalExecGrantID(ctx context.Context, tx workspaceTransaction, input KernelLocalExecApprovalResolutionInput) (string, error) {
	if input.Scope != "conversation" {
		return "local-exec:" + uuid.NewString(), nil
	}
	scope, err := sessionPermissionScopeTx(ctx, tx, input.OwnerUserID, input.FrameID, &SessionPermissionExpectedScope{
		FrameIncarnationID: input.FrameIncarnationID, RootFrameID: input.RootFrameID, RootFrameIncarnationID: input.RootIncarnationID,
	})
	if err != nil {
		return "", err
	}
	if scope.ProjectID != input.ProjectID {
		return "", ErrSessionGrantScopeStale
	}
	key := kernelLocalExecGrantKey(input.Tool, input.Environment)
	return "apg1." + base64.RawURLEncoding.EncodeToString([]byte(scope.RootFrameIncarnationID)) + "." +
		sessionGrantBindingDigest(input.OwnerUserID, scope.ProjectID, scope.RootFrameID, scope.RootFrameIncarnationID, key) + "." + uuid.NewString(), nil
}

func sessionGrantMatchesBinding(id, owner, project, root, incarnation, key string) bool {
	parts := strings.Split(id, ".")
	if len(parts) != 4 || parts[0] != "apg1" {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || string(decoded) != incarnation || parts[2] != sessionGrantBindingDigest(owner, project, root, incarnation, key) {
		return false
	}
	_, err = uuid.Parse(parts[3])
	return err == nil
}
