package workspace

const (
	kernelWorkerStartupV88CallbackID        = "kernel-worker-startup-v88-noop"
	kernelWorkerStartupV88PreflightIdentity = "kernel-worker-startup-v88-journal-only-v1"
	kernelWorkerStartupV88RuleSpec          = "x-science.workspace.kernel-worker-startup.v88"
)

// The startup identity is part of closed durable V1 session metadata. This
// journal-only floor excludes binaries that cannot restore that identity before
// a new session can be accepted. No task, session or execution row is rewritten.
var kernelWorkerStartupV88Migration = versionedSchemaMigration{
	version: 88,
	name:    "kernel-worker-startup-reader-floor",
	identityV2: &schemaMigrationIdentityV2{
		CallbackID:        kernelWorkerStartupV88CallbackID,
		PreflightIdentity: kernelWorkerStartupV88PreflightIdentity,
		RuleSpec:          kernelWorkerStartupV88RuleSpec,
	},
}
