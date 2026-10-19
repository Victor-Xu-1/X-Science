package workspace

import (
	"testing"

	"x-science/internal/sessionagent"
)

func TestSessionSpecialistReadbackUsesCurrentBranchWithoutRewritingReceipt(t *testing.T) {
	store, _, claim := newKernelLocalOperationFixture(t)
	state, err := store.SessionSpecialist(t.Context(), "owner", "frame")
	if err != nil {
		t.Fatal(err)
	}
	definition, err := sessionagent.SealSnapshot(sessionagent.Snapshot{
		AgentName: "SYNTHETIC_A", SelectionName: "SYNTHETIC_A",
		AssistantID: "synthetic-assistant-a", DefinitionID: "synthetic-definition-a",
		Bundled: &sessionagent.BundledDefinition{SystemPrompt: "Synthetic fixture authority."},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := sessionagent.Command{
		Schema: sessionagent.CommandSchema, CommandID: "synthetic-selection",
		Scope: state.Scope, ExpectedRevision: state.Revision, Branch: state.Branch,
		DesiredAgent: definition.SelectionName,
	}
	prepared, err := store.PrepareSessionSpecialist(t.Context(), "owner", "frame", command,
		sessionagent.Preparation{Desired: definition, Fallback: &definition})
	if err != nil {
		t.Fatal(err)
	}
	// Advance only the isolated fixture's existing canonical branch generation,
	// just as a real branch operation does. No production database is accessed.
	if _, err := store.db.Exec(`UPDATE transcript_branch_state SET generation=generation+1 WHERE stream_uid=?`, claim.StreamUID); err != nil {
		t.Fatal(err)
	}
	current, err := store.SessionSpecialist(t.Context(), "owner", "frame")
	if err != nil {
		t.Fatal(err)
	}
	if current.Branch.ID != command.Branch.ID || current.Branch.Generation != command.Branch.Generation+1 {
		t.Fatalf("readback retained stale prepared branch: got=%#v before=%#v", current.Branch, command.Branch)
	}
	if current.PreparedBranch != command.Branch {
		t.Fatal("current branch projection rebound the historical prepared input")
	}
	replayed, found, err := store.ReplaySessionSpecialist(t.Context(), "owner", "frame", command, false)
	if err != nil || !found || !replayed.Replayed {
		t.Fatalf("immutable replay after branch advance: found=%t err=%v result=%#v", found, err, replayed)
	}
	if replayed.Receipt != prepared.Receipt || replayed.Receipt.Command.Branch != command.Branch {
		t.Fatal("readback rewrote the accepted command's historical branch")
	}
	if replayed.Snapshot.Branch != current.Branch {
		t.Fatal("replay returned a stale current-state projection")
	}
}
