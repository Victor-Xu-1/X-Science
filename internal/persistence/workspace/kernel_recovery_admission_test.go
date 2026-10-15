package workspace

import (
	"errors"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func preparedAdmissionOperation(t *testing.T) (*Store, *transcriptstore.Repository, transcriptstore.RunnerClaim, KernelLocalOperation) {
	t.Helper()
	store, repo, claim := newKernelLocalOperationFixture(t)
	operation := createKernelLocalOperationForTest(t, store, repo, claim, "admission-checkpoint", "admission-call")
	approved, err := store.ResolveKernelLocalOperationApproval(t.Context(), ResolveKernelLocalOperationApprovalInput{
		OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
		ApprovalRequestID: operation.ApprovalRequestID, Approved: true, DecisionID: "admission-decision", Scope: "once", Source: "user", ActorID: operation.OwnerUserID, CurrentClaim: claim})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := store.PrepareKernelLocalOperation(t.Context(), PrepareKernelLocalOperationInput{
		OwnerUserID: approved.OwnerUserID, OperationID: approved.OperationID, ExpectedStateVersion: approved.StateVersion, Claim: claim,
		BootID: "admission-boot", KernelID: "admission-kernel", KernelGeneration: 1, ConfinementSHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	return store, repo, claim, prepared
}

func recoveryAdmissionReclaimInput(claim transcriptstore.RunnerClaim, operation KernelLocalOperation) ReclaimPreparedKernelLocalOperationInput {
	return ReclaimPreparedKernelLocalOperationInput{OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
		RunnerID: claim.RunnerID, RunnerAttempt: claim.Attempt, RunnerClaimSHA256: kernelLocalOperationClaimSHA256(claim.ClaimToken),
		BootID: operation.BootID, ReasonCode: "runner_restarted_before_start"}
}

func TestRecoveryAdmissionAfterReclaimSurvivesCurrentRunnerFinish(t *testing.T) {
	store, repo, oldClaim, operation := preparedAdmissionOperation(t)
	if _, err := repo.InterruptRunner(t.Context(), transcriptstore.InterruptRunnerInput{Claim: oldClaim,
		ClientMessageID: "recovery-race-interrupt", ReasonCode: "runtime_draining", AutoResume: true}); err != nil {
		t.Fatal(err)
	}
	current, err := repo.ClaimNextRunner(t.Context(), transcriptstore.ClaimNextRunnerInput{RunnerID: "recovery-race-current", TTL: time.Minute})
	if err != nil || !current.Claimed {
		t.Fatalf("legitimate current claim=%+v %v", current, err)
	}
	recovered, err := store.ReclaimPreparedKernelLocalOperation(t.Context(), recoveryAdmissionReclaimInput(oldClaim, operation))
	if err != nil {
		t.Fatal(err)
	}
	if recovered.AdmittedInputRevision <= current.Claim.ClaimedInputRevision {
		t.Fatalf("no recovery tail: %+v current=%+v", recovered, current.Claim)
	}
	if _, err := repo.ValidateLiveRunnerClaim(t.Context(), current.Claim); err != nil {
		t.Fatalf("current live owner changed: %v", err)
	}
	if _, receipt, _, err := repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{Claim: current.Claim,
		ClientMessageID: "recovery-race-finish", Status: "completed", PayloadJSON: []byte(`{"text":"Prior input finished"}`)}); err != nil || receipt.Status != "completed" {
		t.Fatalf("finish receipt=%+v %v", receipt, err)
	}
	frame, found, err := store.GetFrame(operation.FrameID)
	if err != nil || !found || frame.Status != FrameStatusProcessing {
		t.Errorf("recovery tail terminalized: frame=%+v %v", frame, err)
	}
	next, err := repo.ClaimNextRunner(t.Context(), transcriptstore.ClaimNextRunnerInput{RunnerID: "recovery-race-next", TTL: time.Minute})
	if err != nil || !next.Claimed || next.Claim.ClaimedInputRevision != recovered.AdmittedInputRevision {
		t.Errorf("recovery tail not claimable: %+v %v", next, err)
	}
}

func TestKernelRecoveryAdmissionBindsExactTransitions(t *testing.T) {
	store, repo, claim, prepared := preparedAdmissionOperation(t)
	input := recoveryAdmissionReclaimInput(claim, prepared)
	if _, err := store.ReclaimPreparedKernelLocalOperation(t.Context(), input); !errors.Is(err, ErrKernelLocalOperationStale) {
		t.Fatalf("live reclaim=%v", err)
	}
	if _, err := repo.InterruptRunner(t.Context(), transcriptstore.InterruptRunnerInput{Claim: claim, ClientMessageID: "admission-interrupt", ReasonCode: "runtime_draining", AutoResume: true}); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.ReclaimPreparedKernelLocalOperation(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := store.ReclaimPreparedKernelLocalOperation(t.Context(), input); err != nil || again.StateVersion != recovered.StateVersion {
		t.Fatalf("reclaim replay=%+v %v", again, err)
	}
	coverage, err := repo.GetInputCoverage(t.Context(), claim.StreamUID, claim.OwnerID, claim.ClaimedInputRevision)
	if err != nil || !coverage.Complete || !coverage.HasRecovery || coverage.HasMessage {
		t.Fatalf("reclaim coverage=%+v %v", coverage, err)
	}
	if _, err := transcriptstore.NewRepository(store.db).GetInputCoverage(t.Context(), claim.StreamUID, claim.OwnerID, claim.ClaimedInputRevision); !errors.Is(err, transcriptstore.ErrSchemaUnavailable) {
		t.Fatalf("missing verifier error=%v", err)
	}
	next, err := repo.ClaimNextRunner(t.Context(), transcriptstore.ClaimNextRunnerInput{RunnerID: "recovery-owner", TTL: time.Minute})
	if err != nil || !next.Claimed || next.Claim.ClaimedInputRevision != coverage.InputRevision {
		t.Fatalf("recovery claim=%+v %v", next, err)
	}
	prepared, err = store.PrepareKernelLocalOperation(t.Context(), PrepareKernelLocalOperationInput{OwnerUserID: recovered.OwnerUserID,
		OperationID: recovered.OperationID, ExpectedStateVersion: recovered.StateVersion, Claim: next.Claim, BootID: "admission-boot-2", KernelID: "admission-kernel-2", KernelGeneration: 2, ConfinementSHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	started, err := store.StartKernelLocalOperation(t.Context(), StartKernelLocalOperationInput{OwnerUserID: prepared.OwnerUserID,
		OperationID: prepared.OperationID, ExpectedStateVersion: prepared.StateVersion, Claim: next.Claim, BootID: prepared.BootID, ExecutionID: "admission-execution"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.InterruptRunner(t.Context(), transcriptstore.InterruptRunnerInput{Claim: next.Claim, ClientMessageID: "admission-interrupt-2", ReasonCode: "runtime_draining", AutoResume: true}); err != nil {
		t.Fatal(err)
	}
	unknownInput := MarkKernelLocalOperationOutcomeUnknownInput{OwnerUserID: started.OwnerUserID, OperationID: started.OperationID,
		ExpectedStateVersion: started.StateVersion, ExecutionID: started.ExecutionID, CurrentBootID: "different-boot", ReasonCode: "service_restarted_after_start"}
	unknown, err := store.MarkKernelLocalOperationOutcomeUnknown(t.Context(), unknownInput)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := store.MarkKernelLocalOperationOutcomeUnknown(t.Context(), unknownInput); err != nil || again.StateVersion != unknown.StateVersion {
		t.Fatalf("unknown replay=%+v %v", again, err)
	}
	coverage, err = repo.GetInputCoverage(t.Context(), claim.StreamUID, claim.OwnerID, next.Claim.ClaimedInputRevision)
	if err != nil || !coverage.Complete || !coverage.HasRecovery {
		t.Fatalf("unknown coverage=%+v %v", coverage, err)
	}
	var recoveryFacts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM transcript_input_admissions WHERE stream_uid=? AND source_kind='kernel_recovery'`, claim.StreamUID).Scan(&recoveryFacts); err != nil || recoveryFacts != 2 {
		t.Fatalf("recovery facts=%d %v", recoveryFacts, err)
	}
	// Existing conflicting coordinates must be rejected at read time as well
	// as by the normal immutable writer. This is controlled corruption only.
	if _, err := store.db.Exec(`DROP TRIGGER transcript_input_admissions_immutable; UPDATE transcript_input_admissions SET operation_state_version=operation_state_version+1
		WHERE stream_uid=? AND input_revision=?`, claim.StreamUID, coverage.InputRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetInputCoverage(t.Context(), claim.StreamUID, claim.OwnerID, next.Claim.ClaimedInputRevision); !errors.Is(err, transcriptstore.ErrEventConflict) {
		t.Fatalf("missing exact source accepted: %v", err)
	}
}

func TestKernelRecoveryAdmissionFailureRollsBackItsTransition(t *testing.T) {
	store, repo, claim, prepared := preparedAdmissionOperation(t)
	if _, err := repo.InterruptRunner(t.Context(), transcriptstore.InterruptRunnerInput{Claim: claim, ClientMessageID: "rollback-interrupt", ReasonCode: "runtime_draining", AutoResume: true}); err != nil {
		t.Fatal(err)
	}
	before, err := repo.GetStream(t.Context(), claim.StreamUID, claim.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER injected_admission_failure BEFORE INSERT ON transcript_input_admissions
		WHEN NEW.source_kind='kernel_recovery' BEGIN SELECT RAISE(ABORT,'controlled-admission-failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReclaimPreparedKernelLocalOperation(t.Context(), recoveryAdmissionReclaimInput(claim, prepared)); err == nil || !strings.Contains(err.Error(), "controlled-admission-failure") {
		t.Fatalf("injection=%v", err)
	}
	after, err := repo.GetStream(t.Context(), claim.StreamUID, claim.OwnerID)
	if err != nil || before.InputRevision != after.InputRevision {
		t.Fatalf("counter leaked: before=%+v after=%+v %v", before, after, err)
	}
	retained, found, err := store.GetKernelLocalOperation(t.Context(), claim.OwnerID, prepared.OperationID)
	if err != nil || !found || retained.State != prepared.State || retained.StateVersion != prepared.StateVersion {
		t.Fatalf("transition leaked: %+v %v", retained, err)
	}
	var transitions int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM kernel_local_operation_transitions WHERE operation_id=? AND state_version>?`, prepared.OperationID, prepared.StateVersion).Scan(&transitions); err != nil || transitions != 0 {
		t.Fatalf("receipt leaked: %d %v", transitions, err)
	}
}
