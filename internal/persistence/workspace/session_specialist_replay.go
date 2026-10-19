package workspace

import (
	"context"
	"database/sql"

	"x-science/internal/sessionagent"
)

// Read accepted receipts before invoking mutable catalogs or external settings
// owners. The eventual writer repeats replay checking to close the race.
func (s *Store) ReplaySessionSpecialist(ctx context.Context,owner,frameID string,command sessionagent.Command,host bool) (sessionagent.Mutation,bool,error) {
	var result sessionagent.Mutation
	tx,err:=s.readDatabase().BeginTx(ctx,&sql.TxOptions{ReadOnly:true})
	if err!=nil { return result,false,err }
	defer tx.Rollback()
	scope,err:=sessionagent.ResolveScope(ctx,tx,owner,frameID)
	if err!=nil { return result,false,err }
	if scope!=command.Scope { return result,false,sessionagent.ErrScopeStale }
	receipt,found,err:=sessionagent.LookupReceipt(ctx,tx,owner,scope,command.CommandID)
	if err!=nil || !found { return result,false,err }
	if host {
		if receipt.Command.DesiredAgent!=command.DesiredAgent || receipt.Command.UseDefault!=command.UseDefault { return result,false,sessionagent.ErrCommandConflict }
	} else if receipt.Command!=command { return result,false,sessionagent.ErrCommandConflict }
	result=sessionagent.Mutation{Schema:sessionagent.MutationSchema,Receipt:receipt,Replayed:true}
	result.Snapshot,err=s.sessionSpecialistTx(ctx,tx,owner,frameID)
	if err!=nil { return result,false,err }
	return result,true,tx.Commit()
}
