package server

import (
	"math"
	"testing"
)

func TestComputeProviderReceiptCannotInventSuccessFromMissingValues(t *testing.T) {
	for _, probe := range []map[string]any{
		{}, {"ready": "true"}, {"ready": true}, {"ready": true, "job_exit_code": 0},
		{"ready": true, "job_exit_code": "0", "job_wall_s": 1},
		{"ready": true, "job_exit_code": math.NaN(), "job_wall_s": 1},
		{"ready": true, "job_exit_code": 0, "job_wall_s": -1},
		{"ready": true, "job_exit_code": 0, "job_wall_s": 1, "deadline_fired": "false"},
	} {
		if err := validateComputeProviderProbeReceipt(probe); err == nil {
			t.Fatalf("invalid receipt accepted: %#v", probe)
		}
	}
	for _, probe := range []map[string]any{
		{"ready": false}, {"ready": true, "job_exit_code": 0, "job_wall_s": 1},
		{"ready": true, "job_exit_code": 137, "job_wall_s": 1},
		{"ready": true, "phase_read_error": "explicit malformed terminal record"},
	} {
		if err := validateComputeProviderProbeReceipt(probe); err != nil {
			t.Fatalf("valid observation rejected: %#v %v", probe, err)
		}
	}
}
