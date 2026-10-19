package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/sessionagent"
)

func (s *Store) SessionSpecialist(ctx context.Context, owner, frameID string) (sessionagent.Readback,error) {
	if s==nil || s.readDatabase()==nil || ctx==nil { return sessionagent.Readback{},sessionagent.ErrUnavailable }
	tx,err:=s.readDatabase().BeginTx(ctx,&sql.TxOptions{ReadOnly:true})
	if err!=nil { return sessionagent.Readback{},err }
	defer tx.Rollback()
	result,err:=s.sessionSpecialistTx(ctx,tx,owner,frameID)
	if err!=nil { return result,err }
	return result,tx.Commit()
}

func (s *Store) sessionSpecialistTx(ctx context.Context,tx sessionagent.Transaction,owner,frameID string) (sessionagent.Readback,error) {
	scope,err:=sessionagent.ResolveScope(ctx,tx,owner,frameID)
	if err!=nil { return sessionagent.Readback{},err }
	head,_,found,err:=sessionagent.ReadHead(ctx,tx,owner,scope)
	if err!=nil { return sessionagent.Readback{},err }
	branch,err:=sessionagent.ActiveBranch(ctx,tx,owner,frameID)
	if err!=nil { return sessionagent.Readback{},err }
	result:=sessionagent.ReadbackHead(head)
	if !found { result.Branch=branch }
	err=tx.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM transcript_runner_attempts r
		JOIN transcript_frame_authority a ON a.active_stream_uid=r.stream_uid
		WHERE a.owner_id=? AND a.session_id=? AND r.status='running' AND r.expires_at>?)`,owner,frameID,s.now().UTC()).Scan(&result.HasLiveAttempt)
	return result,err
}

// Capture is invoked only for a new command while the existing immediate
// writer owns admission. Lost-ACK retries do not consult a changed catalog.
func (s *Store) PrepareSessionSpecialist(ctx context.Context, owner, frameID string, command sessionagent.Command, prepared sessionagent.Preparation) (sessionagent.Mutation,error) {
	return s.prepareSessionSpecialist(ctx,owner,frameID,command,prepared,nil,false)
}

func (s *Store) PrepareSessionSpecialistHost(ctx context.Context,owner,frameID string,command sessionagent.Command,prepared sessionagent.Preparation,guard func(*transcriptstore.ImmediateTransaction)error) (sessionagent.Mutation,error) {
	return s.prepareSessionSpecialist(ctx,owner,frameID,command,prepared,guard,true)
}

func (s *Store) prepareSessionSpecialist(ctx context.Context, owner, frameID string, command sessionagent.Command, prepared sessionagent.Preparation,guard func(*transcriptstore.ImmediateTransaction)error,host bool) (sessionagent.Mutation,error) {
	var result sessionagent.Mutation
	if s==nil || s.db==nil || ctx==nil { return result,sessionagent.ErrUnavailable }
	if strings.TrimSpace(owner)=="" || sessionagent.ValidateCommand(command)!=nil { return result,sessionagent.ErrInvalid }
	err:=transcriptstore.NewRepository(s.db).RunImmediate(ctx,func(tx *transcriptstore.ImmediateTransaction) error {
		scope,err:=sessionagent.ResolveScope(ctx,tx,owner,frameID)
		if err!=nil { return err }
		if scope!=command.Scope { return sessionagent.ErrScopeStale }
		if host {
			receipt,found,err:=sessionagent.LookupReceipt(ctx,tx,owner,scope,command.CommandID)
			if err!=nil { return err }
			if found {
				if receipt.Command.DesiredAgent!=command.DesiredAgent || receipt.Command.UseDefault!=command.UseDefault { return sessionagent.ErrCommandConflict }
				command=receipt.Command
			}
		}
		// Immutable replay is handled by Prepare before capture. A branch change
		// still permits that read-only replay; only a new command requires the
		// currently active branch generation.
		result,err=sessionagent.Prepare(ctx,tx,owner,command,func(initial bool)(sessionagent.Preparation,error){
			if guard!=nil { if err:=guard(tx); err!=nil { return sessionagent.Preparation{},err } }
			branch,err:=sessionagent.ActiveBranch(ctx,tx,owner,frameID)
			if err!=nil { return sessionagent.Preparation{},err }
			if branch!=command.Branch { return sessionagent.Preparation{},sessionagent.ErrScopeStale }
			if err:=sessionagent.DefinitionAvailable(ctx,tx,owner,prepared.Desired); err!=nil { return sessionagent.Preparation{},err }
			if initial && prepared.Fallback!=nil {
				if err:=sessionagent.DefinitionAvailable(ctx,tx,owner,*prepared.Fallback); errors.Is(err,sessionagent.ErrTargetUnavailable) { prepared.Fallback=nil;prepared.FallbackUnavailable=true } else if err!=nil { return sessionagent.Preparation{},err }
			}
			return prepared,nil
		},s.now().UTC())
		if err!=nil { return err }
		result.Snapshot,err=s.sessionSpecialistTx(ctx,tx,owner,frameID)
		if err!=nil || result.Replayed { return err }
		var project string
		if err:=tx.QueryRowContext(ctx,`SELECT project_id FROM frames WHERE id=?`,frameID).Scan(&project); err!=nil { return err }
		_,err=s.enqueueRealtimeOutboxTransaction(ctx,tx,RealtimeEventInput{
			ID:sessionagent.RecordID("notification",owner,scope.FrameIncarnationID,command.CommandID),
			UserID:owner,ProjectID:project,RootFrameID:scope.RootFrameID,FrameID:frameID,Type:"conversation.listChanged",
			Payload:map[string]any{"conversation_id":frameID,"action":"updated","source":"xscience","specialist_changed":true,
				"frame_incarnation_id":scope.FrameIncarnationID,"specialist_revision":result.Snapshot.Revision},
		},"","")
		return err
	})
	return result,err
}

// SessionSpecialistDefinition resolves only a private prepared/applied record.
// A caller cannot submit configuration bytes or turn legacy target_agent into
// execution authority. Canonical admission repeats the reference check.
func (s *Store) SessionSpecialistDefinition(ctx context.Context,owner,frameID string,reference *sessionagent.InputReference) (*sessionagent.Snapshot,error) {
	tx,err:=s.readDatabase().BeginTx(ctx,&sql.TxOptions{ReadOnly:true})
	if err!=nil { return nil,err }
	defer tx.Rollback()
	scope,err:=sessionagent.ResolveScope(ctx,tx,owner,frameID)
	if err!=nil { return nil,err }
	head,_,found,err:=sessionagent.ReadHead(ctx,tx,owner,scope)
	if err!=nil { return nil,err }
	if !found && reference==nil { return nil,nil }
	selection:=head.Applied
	if selection.SnapshotID=="" { selection=head.Fallback }
	if reference!=nil {
		branch,err:=sessionagent.ActiveBranch(ctx,tx,owner,frameID)
		if err!=nil { return nil,err }
		matches,err:=sessionagent.ReferenceMatches(ctx,tx,owner,head,*reference,branch)
		if err!=nil { return nil,err }
		if !matches { return nil,sessionagent.ErrRevisionConflict }
		selection=head.Desired
	}
	if selection.SnapshotID=="" { return nil,sessionagent.ErrTargetUnavailable }
	definition,err:=sessionagent.ReadSnapshot(ctx,tx,owner,scope,selection.SnapshotID)
	if err!=nil { return nil,err }
	return &definition,tx.Commit()
}
