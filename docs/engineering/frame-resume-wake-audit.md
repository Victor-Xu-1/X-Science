# Resume wake audit

The existing frame resume dispatcher is the only execution path. Wake evidence
describes why an already eligible registered dispatch had a waiting fence
cleared; it does not grant execution, change approval, reset retry policy, or
prove that the task subsequently ran successfully.

## One waiting period, one winning transition

Requeue atomically records its existing interruption event ID as `waitEventId`.
Callers read that immutable waiting-period reference and pass it as
`expectedWaitRef` with a typed source and cause. The existing transaction matches
the current reference and payload, clears the fence, and appends one wake event
whose identity includes both the resume event and waiting period.

Duplicate or competing causes for the same waiting period retain the winning
audit source and timestamp. A stale reference cannot clear a later wait. A new
claim and requeue get a new interruption/wait identity, so later wakes no longer
reuse the first historical audit event. Audit failure rolls back the fence and
its new cause receipt together.

Old fenced rows without a wait reference use an opaque exact-payload snapshot
identity. A matching original interruption event is linked only when its exact
attempt, recorded coordinates, reason and timestamp match. Missing historical
actor or cause remains unknown; it is never inferred from task text, proximity
in time, or current operation state. Historical events are not rewritten.

## Sources and evidence

| Source | Witness |
| --- | --- |
| Continue request | Authenticated frame owner and a new accepted-request receipt bound to the observed wait |
| Kernel operation | Committed operation identity/version when available, with decision identity/source/actor as decision metadata, not a claim that every wake was a human approval |
| Kernel settlement | Immutable settlement outbox ID, expected operation version, notification ID, and observed committed operation version when available |
| Automatic recovery | Exact stream, runner attempt and checkpoint sequence; the real checkpoint event ID is resolved without substituting a different latest checkpoint |
| Changed recovery condition | Exact new user/input/material checkpoint event, or observed old/new recovery-contract revisions and available runtime build metadata |
| Task-operation settlement | Validated outbox operation and notification IDs, recorded inside the original settlement transaction |

Unavailable optional operation observations are explicit unknown evidence; they
do not invent a committed transition or prevent the original eligible wake.
The compact schema is diagnostic and carries identifiers, not model reasoning,
task prose, tool arguments, secrets, or research output.

Model selection keeps its separate revision-bound armed/consume path. A
registered wait joins the same single-winner transition identity; an active
claim retains distinct armed revision events and the consumed armed-event
reference. A mutable model revision is labeled observed evidence, not an
immutable model-change command receipt.

## Boundaries

An accepted Continue request is not proof of a physical human click. The
current HTTP request has no stable client command key or original wait
reference, so this audit contract does not claim cross-wait HTTP retry
idempotency. That requires a separate request-protocol change.

Checkpoint sequence and transcript event ID are distinct coordinates. This
audit change resolves exact checkpoint witnesses but does not migrate the
older dispatch coordinate representation or change automatic recovery
eligibility. Coordinate cleanup is separate from a demonstrated false-wake
incident; the mislabeled field alone is not evidence that one occurred.

Focused regressions live in `frame_resume_wake_audit_test.go` (real SQLite)
and `frame_resume_wake_evidence_test.go` (server caller/HTTP evidence), plus the
existing approval, recovery-condition and dispatch compatibility regressions.
These do not establish a 24-hour endurance run or a successful scientific task.
