package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
)

type computeNamespaceSource struct {
	JobID, OwnerID, ProjectID, FrameID, RootFrameID, Environment, Provider, ExternalID string
	Intent                                                                             []byte
	Hardware                                                                           []byte
}

type ComputeNamespaceCutoverReceipt struct {
	JobID                            string
	OwnerUserID                      string
	OriginalState                    string
	SourceSHA256                     string
	Environment                      string
	Provider                         string
	ProjectID                        string
	FrameID                          string
	RootFrameID                      string
	ExternalID                       string
	InputSHA256                      string
	OriginalOutputManifestSHA256     string
	OriginalCheckpointManifestSHA256 string
}

const computeNamespaceSourceColumns = `job_id,owner_user_id,project_id,COALESCE(frame_id,''),COALESCE(root_frame_id,''),environment,provider,COALESCE(external_id,''),COALESCE(intent_json,''),COALESCE(hardware_json,'')`

func computeNamespaceSourceDigest(source computeNamespaceSource) string {
	source.Hardware = computeNamespaceSubmissionHardware(source.Hardware)
	return computeNamespaceRawSourceDigest(source)
}

func computeNamespaceRawSourceDigest(source computeNamespaceSource) string {
	encoded, _ := json.Marshal(source)
	digest := sha256.Sum256(append([]byte("x-science.namespace-original-compute-job.v1\x00"), encoded...))
	return hex.EncodeToString(digest[:])
}

// These are the submission-authority fields from the BYOC/checkpoint contract.
// Polling, logs, checkpoint-generation/elapsed counters and harvest results do
// not change the admitted identity. Original full raw digests remain recorded.
func computeNamespaceSubmissionHardware(raw []byte) []byte {
	if len(raw) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return raw
	}
	projection := map[string]json.RawMessage{}
	for _, key := range []string{"provider_params", "outputs", "workspace_dir", "install_id", "submission_id", "handle_id", "sandbox_hint", "staging_dir", "archive_sha256", "archive_bytes", "provider_spec", "provider_spec_sha256", "provider_config_sha256", "exclude", "transfer_limits", "root_frame_incarnation_id", "job_timeout_seconds", "sandbox_deadline_epoch", "harvest_margin_seconds", "termination_grace_seconds", "checkpoint_contract", "logical_timeout_seconds", "managed_ssh", "remote_workdir", "timeout_seconds", "scheduler", "scheduler_directives", "remote_inputs"} {
		if value, found := fields[key]; found {
			projection[key] = value
		}
	}
	encoded, _ := json.Marshal(projection)
	return encoded
}

func scanComputeNamespaceSource(row interface{ Scan(...any) error }) (computeNamespaceSource, error) {
	var source computeNamespaceSource
	err := row.Scan(&source.JobID, &source.OwnerID, &source.ProjectID, &source.FrameID, &source.RootFrameID, &source.Environment, &source.Provider, &source.ExternalID, &source.Intent, &source.Hardware)
	return source, err
}

func captureNamespaceComputeReceipts(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT `+computeNamespaceSourceColumns+`,state,COALESCE((SELECT result FROM compute_usage usage WHERE usage.job_id=compute_workbench_jobs.job_id),'') FROM compute_workbench_jobs ORDER BY job_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var source computeNamespaceSource
		var state string
		var result string
		if err := rows.Scan(&source.JobID, &source.OwnerID, &source.ProjectID, &source.FrameID, &source.RootFrameID, &source.Environment, &source.Provider, &source.ExternalID, &source.Intent, &source.Hardware, &state, &result); err != nil {
			return err
		}
		outputSHA, checkpointSHA, err := computeNamespaceResultDigests([]byte(result))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO namespace_compute_job_receipts(job_id,original_state,source_sha256,original_raw_sha256,original_external_id,original_output_manifest_sha256,original_checkpoint_manifest_sha256) VALUES(?,?,?,?,?,?,?)`, source.JobID, state, computeNamespaceSourceDigest(source), computeNamespaceRawSourceDigest(source), source.ExternalID, outputSHA, checkpointSHA); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, statement := range []string{
		`CREATE TRIGGER namespace_compute_job_receipts_insert_guard BEFORE INSERT ON namespace_compute_job_receipts BEGIN SELECT RAISE(ABORT,'namespace compute cutover inventory is sealed'); END`,
		`CREATE TRIGGER namespace_compute_job_receipts_update_guard BEFORE UPDATE ON namespace_compute_job_receipts BEGIN SELECT RAISE(ABORT,'namespace compute cutover inventory is immutable'); END`,
		`CREATE TRIGGER namespace_compute_job_receipts_delete_guard BEFORE DELETE ON namespace_compute_job_receipts BEGIN SELECT RAISE(ABORT,'namespace compute cutover inventory is immutable'); END`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetComputeNamespaceCutoverReceipt(ctx context.Context, ownerID, jobID string) (ComputeNamespaceCutoverReceipt, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.db == nil {
		return ComputeNamespaceCutoverReceipt{}, false, errors.New("workspace store is closed")
	}
	if !namespaceIdentityValid(ownerID) || !namespaceIdentityValid(jobID) {
		return ComputeNamespaceCutoverReceipt{}, false, errors.New("owner and compute job identity are required")
	}
	return getComputeNamespaceCutoverReceiptQuery(ctx, s.db, ownerID, jobID)
}

type computeNamespaceQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getComputeNamespaceCutoverReceiptQuery(ctx context.Context, query computeNamespaceQueryer, ownerID, jobID string) (ComputeNamespaceCutoverReceipt, bool, error) {
	source, err := scanComputeNamespaceSource(query.QueryRowContext(ctx, `SELECT `+computeNamespaceSourceColumns+` FROM compute_workbench_jobs WHERE job_id=? AND owner_user_id=?`, jobID, ownerID))
	if errors.Is(err, sql.ErrNoRows) {
		return ComputeNamespaceCutoverReceipt{}, false, nil
	}
	if err != nil {
		return ComputeNamespaceCutoverReceipt{}, false, err
	}
	var state, digest, originalExternalID, outputSHA, checkpointSHA string
	err = query.QueryRowContext(ctx, `SELECT original_state,source_sha256,original_external_id,original_output_manifest_sha256,original_checkpoint_manifest_sha256 FROM namespace_compute_job_receipts WHERE job_id=?`, jobID).Scan(&state, &digest, &originalExternalID, &outputSHA, &checkpointSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return ComputeNamespaceCutoverReceipt{}, false, nil
	}
	if err != nil {
		return ComputeNamespaceCutoverReceipt{}, false, err
	}
	if originalExternalID == "" {
		if state == string(ComputeJobStaging) || state == string(ComputeJobQueued) {
			return ComputeNamespaceCutoverReceipt{}, false, nil // New canonical execution has no historical runtime authority.
		}
		return ComputeNamespaceCutoverReceipt{}, false, errors.New("historical compute runtime identity is unconfirmed")
	}
	if digest != computeNamespaceSourceDigest(source) {
		return ComputeNamespaceCutoverReceipt{}, false, errors.New("historical compute source identity changed after namespace cutover")
	}
	var hardware map[string]json.RawMessage
	inputSHA256 := ""
	if json.Unmarshal(source.Hardware, &hardware) == nil {
		_ = json.Unmarshal(hardware["archive_sha256"], &inputSHA256)
	}
	if !validLowerHexSHA256(inputSHA256) {
		inputSHA256 = ""
	}
	return ComputeNamespaceCutoverReceipt{JobID: jobID, OwnerUserID: source.OwnerID, OriginalState: state, SourceSHA256: digest, Environment: source.Environment,
		Provider: source.Provider, ProjectID: source.ProjectID, FrameID: source.FrameID, RootFrameID: source.RootFrameID, ExternalID: source.ExternalID, InputSHA256: inputSHA256, OriginalOutputManifestSHA256: outputSHA, OriginalCheckpointManifestSHA256: checkpointSHA}, true, nil
}

func computeNamespaceResultDigests(raw []byte) (string, string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", "", nil
	}
	if len(raw) > 1<<20 {
		return "", "", errors.New("historical compute result exceeds bounded receipt contract")
	}
	var result struct {
		OutputSHA  json.RawMessage `json:"output_manifest_sha256"`
		Checkpoint struct {
			Receipt struct {
				SHA json.RawMessage `json:"manifest_sha256"`
			} `json:"receipt"`
		} `json:"checkpoint"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", "", errors.New("historical compute result metadata is invalid")
	}
	decode := func(raw json.RawMessage) (string, error) {
		if len(raw) == 0 || string(raw) == "null" {
			return "", nil
		}
		var value string
		if json.Unmarshal(raw, &value) != nil || (value != "" && !validLowerHexSHA256(value)) {
			return "", errors.New("historical compute manifest digest is invalid")
		}
		return value, nil
	}
	output, err := decode(result.OutputSHA)
	if err != nil {
		return "", "", err
	}
	checkpoint, err := decode(result.Checkpoint.Receipt.SHA)
	return output, checkpoint, err
}
