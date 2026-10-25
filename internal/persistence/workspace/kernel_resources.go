package workspace

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/toolprogress"
)

type KernelResourceReservation struct {
	BackendID      string
	Generation     int64
	BootID         string
	RequestedBytes int64
	ReservedBytes  int64
	State          string
	Admission      *toolprogress.ResourceAdmissionSnapshot
}

// CountPendingKernelResourceWaits projects the existing startup supervisor's
// waiting authority. It neither admits resources nor starts a cancelled call.
func (s *Store) CountPendingKernelResourceWaits(ctx context.Context, access KernelFrameAccess) (int, error) {
	if s == nil || s.db == nil || ctx == nil {
		return 0, errors.New("workspace resource wait authority is unavailable")
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM kernel_resource_reservations r
		JOIN kernel_execution_backends b ON b.backend_id=r.backend_id
			AND b.backend_generation=r.backend_generation AND b.machine_boot_id=r.machine_boot_id
		JOIN frames f ON f.id=b.frame_id AND f.incarnation_id=b.frame_incarnation_id
		JOIN frames root ON root.id=b.root_frame_id AND root.incarnation_id=b.root_frame_incarnation_id
		JOIN projects p ON p.id=b.project_id AND p.user_id=b.owner_user_id
		WHERE r.state IN ('waiting','reserved') AND b.state='starting' AND f.status='processing'
			AND f.project_id=p.id AND root.project_id=p.id
			AND b.owner_user_id=? AND b.project_id=? AND b.frame_id=? AND b.frame_incarnation_id=?
			AND b.root_frame_id=? AND b.root_frame_incarnation_id=?`, access.UserID, access.Frame.ProjectID,
		access.Frame.ID, access.Frame.IncarnationID, access.Frame.RootFrameID, access.RootFrameIncarnationID).Scan(&count)
	return count, err
}

// Capacity excludes the control-plane reserve. Usage is a current physical
// observation for the exact reserved generation. Unknown usage is zero, so a
// control outage cannot free a promise. Already-used memory is not counted
// twice against MemAvailable.
type KernelResourceCapacity struct {
	MaximumBytes   int64
	AvailableBytes int64
	DefaultBytes   int64
	MinimumBytes   int64
	Usage          map[string]int64
}

func (s *Store) ListKernelResourceReservations(ctx context.Context, bootID string) ([]KernelResourceReservation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT backend_id,backend_generation,machine_boot_id,requested_bytes,reserved_bytes,state FROM kernel_resource_reservations WHERE machine_boot_id=? AND state!='released' ORDER BY sequence`, bootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []KernelResourceReservation
	for rows.Next() {
		var item KernelResourceReservation
		if err := rows.Scan(&item.BackendID, &item.Generation, &item.BootID, &item.RequestedBytes, &item.ReservedBytes, &item.State); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) KernelResourceReservation(ctx context.Context, id string, generation int64) (KernelResourceReservation, bool, error) {
	var result KernelResourceReservation
	var payload sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT r.backend_id,r.backend_generation,r.machine_boot_id,r.requested_bytes,r.reserved_bytes,r.state,n.payload_json
		FROM kernel_resource_reservations r LEFT JOIN notifications n ON n.id=? AND n.notification_type='resource_capacity_admitted'
		WHERE r.backend_id=? AND r.backend_generation=?`, kernelResourceAdmissionNotificationID(id, generation), id, generation).Scan(&result.BackendID, &result.Generation, &result.BootID, &result.RequestedBytes, &result.ReservedBytes, &result.State, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return result, false, nil
	}
	if err == nil && payload.Valid {
		if observation := decodeKernelResourceAdmissionSnapshot(payload.String); observation != nil &&
			observation.Reason == toolprogress.ResourceAdmissionGranted && observation.RequestedBytes == result.RequestedBytes &&
			observation.ReservedBytes == result.ReservedBytes {
			result.Admission = observation
		}
	}
	return result, err == nil, err
}

func (s *Store) AdmitKernelResources(ctx context.Context, backend KernelExecutionBackend, requested int64, capacity KernelResourceCapacity) (reservation KernelResourceReservation, admitted bool, resultErr error) {
	if ctx == nil || requested < 0 || (requested > 0 && requested < capacity.MinimumBytes) || capacity.AvailableBytes < 0 || capacity.MinimumBytes <= 0 || capacity.DefaultBytes < capacity.MinimumBytes {
		return reservation, false, errors.New("kernel resource capacity contract invalid")
	}
	repository, err := s.TranscriptRepository(ctx)
	if err != nil {
		return reservation, false, err
	}
	noticeAdded := false
	err = repository.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		var generation int64
		var boot, state string
		if err := tx.QueryRowContext(ctx, `SELECT backend_generation,machine_boot_id,state FROM kernel_execution_backends WHERE backend_id=?`, backend.BackendID).Scan(&generation, &boot, &state); err != nil {
			return err
		}
		if generation != backend.BackendGeneration || boot != backend.MachineBootID || state != KernelExecutionBackendStateStarting {
			return ErrKernelExecutionBackendStale
		}
		var owner, frameIncarnation, rootIncarnation, frameStatus string
		if err := tx.QueryRowContext(ctx, `SELECT p.user_id,f.incarnation_id,r.incarnation_id,f.status FROM projects p JOIN frames f ON f.project_id=p.id AND f.id=? JOIN frames r ON r.project_id=p.id AND r.id=? WHERE p.id=?`, backend.FrameID, backend.RootFrameID, backend.ProjectID).Scan(&owner, &frameIncarnation, &rootIncarnation, &frameStatus); err != nil || owner != backend.OwnerUserID || frameIncarnation != backend.FrameIncarnationID || rootIncarnation != backend.RootFrameIncarnationID {
			return ErrKernelExecutionBackendStale
		}
		now := s.now().UTC().Format(time.RFC3339Nano)
		inserted, err := tx.ExecContext(ctx, `INSERT INTO kernel_resource_reservations(backend_id,backend_generation,machine_boot_id,requested_bytes,reserved_bytes,state,created_at,updated_at) VALUES(?,?,?,?,0,'waiting',?,?) ON CONFLICT(backend_id,backend_generation) DO NOTHING`, backend.BackendID, generation, boot, requested, now, now)
		if err != nil {
			return err
		}
		created, err := inserted.RowsAffected()
		if err != nil {
			return err
		}
		var sequence int64
		if err := tx.QueryRowContext(ctx, `SELECT sequence,requested_bytes,reserved_bytes,state FROM kernel_resource_reservations WHERE backend_id=? AND backend_generation=?`, backend.BackendID, generation).Scan(&sequence, &reservation.RequestedBytes, &reservation.ReservedBytes, &reservation.State); err != nil {
			return err
		}
		reservation.BackendID, reservation.Generation, reservation.BootID = backend.BackendID, generation, boot
		if reservation.RequestedBytes != requested {
			return errors.New("kernel resource demand changed within one generation")
		}
		if reservation.State == "reserved" {
			admitted = true
			return nil
		}
		if reservation.State != "waiting" {
			return ErrKernelExecutionBackendStale
		}
		var preceding int
		maximum := capacity.MaximumBytes
		if maximum <= 0 {
			maximum = math.MaxInt64
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM kernel_resource_reservations r JOIN kernel_execution_backends b ON b.backend_id=r.backend_id AND b.backend_generation=r.backend_generation JOIN frames f ON f.id=b.frame_id WHERE r.machine_boot_id=? AND r.state='waiting' AND r.sequence<? AND b.state='starting' AND f.status='processing' AND r.requested_bytes<=?`, boot, sequence, maximum).Scan(&preceding); err != nil {
			return err
		}
		promised, err := kernelUnusedResourcePromises(ctx, tx, boot, capacity.Usage)
		if err != nil {
			if preceding > 0 {
				// Fairness already decides this wait. Optional observation failure
				// must not replace that existing decision with an execution error.
				return nil
			}
			return err
		}
		available := max(0, capacity.AvailableBytes-promised)
		want := requested
		if want == 0 {
			want = min(capacity.DefaultBytes, available)
		}
		want = max(want, capacity.MinimumBytes)
		// Observe the exact inputs used by this serialized decision. This DTO is
		// telemetry only; the original fairness and allocation checks stay below.
		observation := kernelResourceAdmissionSnapshot(now, requested, want, capacity.AvailableBytes, promised, available, preceding)
		if preceding > 0 {
			reservation.Admission = observation
			return nil
		}
		if want > available {
			reservation.Admission = observation
			return nil
		}
		// Only recovery of a previously persisted resource wait requires the
		// processing precondition already used by its startup supervisor. Keep
		// initial/manual admissions and reserved replays on their old contract.
		if created == 0 && frameStatus != "processing" {
			return ErrKernelExecutionBackendStale
		}
		if _, err := tx.ExecContext(ctx, `UPDATE kernel_resource_reservations SET reserved_bytes=?,state='reserved',updated_at=? WHERE backend_id=? AND backend_generation=? AND state='waiting'`, want, now, backend.BackendID, generation); err != nil {
			return err
		}
		if created == 0 {
			// Capacity admission is a durable fact, not readiness or execution.
			// Its existing notice must commit with the promise so startup cannot
			// become invisible between reservation and a later publication.
			if _, _, err := createNotificationTx(ctx, tx, CreateNotificationInput{
				ID:            kernelResourceAdmissionNotificationID(backend.BackendID, generation),
				SenderFrameID: backend.FrameID, RecipientFrameID: backend.FrameID, RootFrameID: backend.RootFrameID,
				OwnerUserID: backend.OwnerUserID, NotificationType: "resource_capacity_admitted",
				Payload: map[string]any{"backend_id": backend.BackendID, "backend_generation": generation,
					"kernel_id": backend.KernelID, "executed": false, "status": "execution_capacity_admitted", "memory_budget_bytes": requested,
					"resource_admission": observation},
			}, s.now().UTC()); err != nil {
				return err
			}
			noticeAdded = true
		}
		reservation.ReservedBytes, reservation.State, admitted = want, "reserved", true
		reservation.Admission = observation
		return nil
	})
	if err != nil {
		admitted = false
		reservation.Admission = nil
	} else if noticeAdded {
		s.signalOutboxWake()
	}
	return reservation, admitted, err
}

func KernelResourceUsageKey(id string, generation int64) string {
	return id + ":" + strconv.FormatInt(generation, 10)
}

// Call only after the original process/supervisor positively proves absence.
// No age, lease timeout or transient control error supplies that evidence.
func (s *Store) ReleaseKernelResources(ctx context.Context, id string, generation int64, bootID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE kernel_resource_reservations SET state='released',updated_at=? WHERE backend_id=? AND backend_generation=? AND machine_boot_id=? AND state!='released'`, s.now().UTC().Format(time.RFC3339Nano), id, generation, bootID)
	return err
}

// A waiting row has no physical launch authority. CAS prevents cancellation
// racing an admission that already reserved/started the same generation.
func (s *Store) CancelWaitingKernelResources(ctx context.Context, id string, generation int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE kernel_resource_reservations SET state='released',updated_at=? WHERE backend_id=? AND backend_generation=? AND state='waiting'`, s.now().UTC().Format(time.RFC3339Nano), id, generation)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
