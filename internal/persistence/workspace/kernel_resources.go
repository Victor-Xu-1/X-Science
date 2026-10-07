package workspace

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"time"

	transcriptstore "synon-go/internal/persistence/transcript"
)

type KernelResourceReservation struct {
	BackendID      string
	Generation     int64
	BootID         string
	RequestedBytes int64
	ReservedBytes  int64
	State          string
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
	err := s.db.QueryRowContext(ctx, `SELECT backend_id,backend_generation,machine_boot_id,requested_bytes,reserved_bytes,state FROM kernel_resource_reservations WHERE backend_id=? AND backend_generation=?`, id, generation).Scan(&result.BackendID, &result.Generation, &result.BootID, &result.RequestedBytes, &result.ReservedBytes, &result.State)
	if errors.Is(err, sql.ErrNoRows) {
		return result, false, nil
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
	err = repository.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		var generation int64
		var boot, state string
		if err := tx.QueryRowContext(ctx, `SELECT backend_generation,machine_boot_id,state FROM kernel_execution_backends WHERE backend_id=?`, backend.BackendID).Scan(&generation, &boot, &state); err != nil {
			return err
		}
		if generation != backend.BackendGeneration || boot != backend.MachineBootID || state != KernelExecutionBackendStateStarting {
			return ErrKernelExecutionBackendStale
		}
		var owner, frameIncarnation, rootIncarnation string
		if err := tx.QueryRowContext(ctx, `SELECT p.user_id,f.incarnation_id,r.incarnation_id FROM projects p JOIN frames f ON f.project_id=p.id AND f.id=? JOIN frames r ON r.project_id=p.id AND r.id=? WHERE p.id=?`, backend.FrameID, backend.RootFrameID, backend.ProjectID).Scan(&owner, &frameIncarnation, &rootIncarnation); err != nil || owner != backend.OwnerUserID || frameIncarnation != backend.FrameIncarnationID || rootIncarnation != backend.RootFrameIncarnationID {
			return ErrKernelExecutionBackendStale
		}
		now := s.now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `INSERT INTO kernel_resource_reservations(backend_id,backend_generation,machine_boot_id,requested_bytes,reserved_bytes,state,created_at,updated_at) VALUES(?,?,?,?,0,'waiting',?,?) ON CONFLICT(backend_id,backend_generation) DO NOTHING`, backend.BackendID, generation, boot, requested, now, now); err != nil {
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
		if preceding > 0 {
			return nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT backend_id,backend_generation,reserved_bytes FROM kernel_resource_reservations WHERE machine_boot_id=? AND state='reserved'`, boot)
		if err != nil {
			return err
		}
		promised := int64(0)
		for rows.Next() {
			var id string
			var gen, bytes int64
			if err := rows.Scan(&id, &gen, &bytes); err != nil {
				_ = rows.Close()
				return err
			}
			usage := max(0, capacity.Usage[KernelResourceUsageKey(id, gen)])
			unused := max(0, bytes-usage)
			if unused > math.MaxInt64-promised {
				_ = rows.Close()
				return errors.New("kernel resource reservation overflow")
			}
			promised += unused
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		available := max(0, capacity.AvailableBytes-promised)
		want := requested
		if want == 0 {
			want = min(capacity.DefaultBytes, available)
		}
		want = max(want, capacity.MinimumBytes)
		if want > available {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE kernel_resource_reservations SET reserved_bytes=?,state='reserved',updated_at=? WHERE backend_id=? AND backend_generation=? AND state='waiting'`, want, now, backend.BackendID, generation); err != nil {
			return err
		}
		reservation.ReservedBytes, reservation.State, admitted = want, "reserved", true
		return nil
	})
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
