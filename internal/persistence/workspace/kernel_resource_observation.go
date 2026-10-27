package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"

	"x-science/internal/toolprogress"
)

func kernelUnusedResourcePromises(ctx context.Context, tx workspaceTransaction, boot string, usage map[string]int64) (int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT backend_id,backend_generation,reserved_bytes FROM kernel_resource_reservations WHERE machine_boot_id=? AND state='reserved'`, boot)
	if err != nil {
		return 0, err
	}
	promised := int64(0)
	for rows.Next() {
		var id string
		var generation, reserved int64
		if err := rows.Scan(&id, &generation, &reserved); err != nil {
			_ = rows.Close()
			return 0, err
		}
		unused := max(0, reserved-max(0, usage[KernelResourceUsageKey(id, generation)]))
		if unused > math.MaxInt64-promised {
			_ = rows.Close()
			return 0, errors.New("kernel resource reservation overflow")
		}
		promised += unused
	}
	return promised, errors.Join(rows.Err(), rows.Close())
}

func kernelResourceAdmissionNotificationID(id string, generation int64) string {
	return "kernel-resources-" + id + "-" + strconv.FormatInt(generation, 10)
}

func kernelResourceAdmissionSnapshot(decisionAt string, requested, required, available, promised, net int64, preceding int) *toolprogress.ResourceAdmissionSnapshot {
	reason := toolprogress.ResourceAdmissionGranted
	if preceding > 0 {
		reason = toolprogress.ResourceAdmissionFairness
	} else if required > net {
		reason = toolprogress.ResourceAdmissionCapacity
	}
	reserved := int64(0)
	if reason == toolprogress.ResourceAdmissionGranted {
		reserved = required
	}
	return toolprogress.NormalizeResourceAdmission(&toolprogress.ResourceAdmissionSnapshot{
		DecisionAt: decisionAt, Reason: reason, RequestedBytes: requested, RequiredBytes: required,
		CapacityAvailableBytes: available, UnusedPromisedBytes: promised,
		NetAvailableBytes: net, ReservedBytes: reserved, PrecedingWaiters: int64(preceding),
	})
}

// The original admitted notification is the sole durable owner of this optional
// observation. Legacy/malformed telemetry never changes a reservation's state.
func decodeKernelResourceAdmissionSnapshot(payload string) *toolprogress.ResourceAdmissionSnapshot {
	if len(payload) == 0 || len(payload) > 4096 {
		return nil
	}
	var value struct {
		Admission *toolprogress.ResourceAdmissionSnapshot `json:"resource_admission"`
	}
	if json.Unmarshal([]byte(payload), &value) != nil {
		return nil
	}
	return toolprogress.NormalizeResourceAdmission(value.Admission)
}
