package workspace

// These statements belong to one additive versioned migration. The controller
// allocates/registers its version against actual main; do not call these from
// request-time schema repair or edit any published migration identity.
var managedEndpointOwnerSchemaStatements = []string{
	`ALTER TABLE compute_managed_endpoints ADD COLUMN owner_spec_json TEXT`,
	`CREATE TABLE compute_managed_endpoint_instances (
		instance_id TEXT PRIMARY KEY, owner_user_id TEXT NOT NULL, endpoint_name TEXT NOT NULL,
		approved_hash TEXT NOT NULL, start_generation INTEGER NOT NULL CHECK(start_generation>0),
		instance_json TEXT NOT NULL, created_at TIMESTAMP NOT NULL, retired_at TIMESTAMP,
		FOREIGN KEY(endpoint_name) REFERENCES compute_managed_endpoints(name) ON DELETE RESTRICT
	)`,
	`CREATE UNIQUE INDEX compute_managed_endpoint_active_owner_idx ON compute_managed_endpoint_instances(endpoint_name) WHERE retired_at IS NULL`,
	`CREATE TABLE compute_managed_endpoint_actions (
		instance_id TEXT NOT NULL REFERENCES compute_managed_endpoint_instances(instance_id) ON DELETE RESTRICT,
		kind TEXT NOT NULL CHECK(kind IN ('start','stop')), claim_generation INTEGER NOT NULL CHECK(claim_generation>0),
		claim_handle TEXT NOT NULL, cancel_requested INTEGER NOT NULL DEFAULT 0 CHECK(cancel_requested IN (0,1)),
		accepted INTEGER NOT NULL DEFAULT 0 CHECK(accepted IN (0,1)), receipt_json TEXT, result_state TEXT, observation_error TEXT,
		created_at TIMESTAMP NOT NULL, finished_at TIMESTAMP,
		PRIMARY KEY(instance_id,kind,claim_generation)
	)`,
	`CREATE INDEX compute_managed_endpoint_pending_action_idx ON compute_managed_endpoint_actions(finished_at,instance_id) WHERE finished_at IS NULL`,
}
