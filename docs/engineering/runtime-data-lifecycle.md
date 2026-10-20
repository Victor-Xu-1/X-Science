# Runtime data lifecycle

X-Science has one task-history authority and one bounded operational-state
store:

- `workspace/x-science.sqlite` owns projects, frames, canonical
  transcript messages, execution evidence, tool records, memories, and artifact
  lineage.
- `runtime-state.sqlite` owns non-conversation operational state such as
  approvals, connector projections, short-lived audits, and usage counters.
  Each key is one SQLite row; updates never rewrite unrelated entries.
- `logs/server-YYYYMMDD.log` is a diagnostic stream, not task history.

The server performs its first lifecycle sweep five minutes after startup and
then once every 24 hours. It logs a compact receipt only when rows were removed.

| Data class | Retention | Protection |
|---|---:|---|
| Unreferenced content snapshots | 1 hour | Hashes referenced by artifact provenance remain |
| Terminal realtime replay rows | 1 hour | Active roots and history activation fences remain |
| Ephemeral artifacts | 1 day | Active roots and all non-ephemeral/final artifacts remain |
| Resolved queued input | 7 days | Queued/delivering input and typed lifecycle receipts remain |
| Delivered transport receipts | 1 hour | Undelivered rows and referenced compute submissions remain |
| Dead letters and operational audits | 7 days | Pending/inflight transport and active state remain |
| Completed compute usage and usage audits | 30 days | Running compute remains |
| Superseded memories | 90 days | Active memory and safe supersession chains remain |
| Daily operational logs | 7 days | Current files remain private mode `0600` |
| Schema rollback snapshots | newest 2 | A newly created migration backup is retained |
| Successful project identity reservations | permanent | Project deletion must not allow an ID to change entities |

Deleting a task or project removes its SQLite child records, realtime replay
cohort, outbox cohort, artifact blobs, compatibility session/journal projection,
and scoped runtime-state entries. The deletion notification is enqueued only
after the retired transport cohort has been removed, so it is the sole event
that survives the transaction.

The lifecycle never applies a fixed age limit to active tasks. It does not
delete conversation messages, execution logs attached to a retained frame,
final artifacts, user uploads, active memory, unresolved approvals, pending
outbox events, credentials, settings, or account data.

## Conversation operation receipts

An Aside creation freezes its parent, owner, parent incarnation, request,
selected source branch/generation, model and intent ID. Its accepted child,
canonical input and copied publication cutoff are committed with the child
history. Repeating the same intent returns that receipt; changed input is a
conflict. Reading an absent receipt does not prove cancellation. The Aside
DELETE endpoint either records cancellation before creation or cancels the
confirmed exact child input. Closing the panel preserves the permanent task
history; it does not delete the child or cancel an unrelated later input.

Normal message acceptance has one authority: typed `messageIntent` metadata
on the canonical user event. The complete normalized request body is bound by
a SHA-256 digest. The existing queue stores a retracted typed receipt for
`DELETE /api/conversations/{id}/message-intents/{loading_id}`, using the same
frozen body as the original POST. This distinguishes
`cancelled_before_acceptance`, `cancelled`, and `already_terminal`; only the
first two confirm a cancellation. The accepted canonical input ID need not be
the loading ID. A client must not derive the ID or substitute the latest input.

Final append, branch append, queued delivery and message-triggered resume all
check the same receipt within their write transaction. A delayed POST or resume
cannot revive that cancelled mutation. A preaccept cancellation does not stop
another running input. Historical records lacking typed proof remain readable
and use their existing replay contract, but exact intent cancellation reports
unavailable rather than inventing proof. These contracts add no schema or
separate scheduler.

Validated Aside lifecycle and message-cancellation receipts remain for the
owning frame lifetime, not the ordinary seven-day transport retention. A bare
marker does not qualify. Sweep classification reads outside the write lock,
then deletion rechecks the exact row identity, payload, state and cutoff.
Explicit frame/project deletion retains its existing scoped foreign-key and
data-removal semantics; deployment never uses it to make a paused task vanish.

### Manual-review terminal receipts

A reviewer can stop without changing the already completed task result. Its
existing finished-event receipt may carry the optional closed
`public_stop_reason`: `no_progress`, `source_changed`, `cancelled`,
`model_protocol`, `provider_unavailable`, or `unknown`. The reason is committed
with the same runner terminal compare-and-swap and operation receipt; a retry
cannot rewrite an earlier terminal reason. Missing reasons keep the historical
payload unchanged and are not backfilled from logs or model prose.

The public reviewer projection accepts a reason only from the matching owner's
reviewer stream, claimed input revision, attempt and finished-event identity.
Malformed, absent or unprovable historical reasons display as unknown; storage
read errors remain errors, not invented diagnoses. The UI translates only this
closed code, never private exception text or provider responses. It distinguishes
an unfinished additional review from failure of the completed task, preserving
its results. A new review remains an explicit Add → Manual review action; this
metadata does not auto-retry, auto-approve or create a second execution authority.
No database schema or retention change is introduced.

### Auto-review preference observation and admission

The existing session-options owner holds the Auto-review value and its freshness;
desktop and mobile share that owner. An attempted persistent write invalidates
cached observations even when the acknowledgement is lost or returns an error.
An unknown result is not displayed as a confirmed checked/unchecked choice and
Retry reads the checked actual root with GET; it does not repeat the mutation.
A readback proves the current observed value, not cancellation or final settlement
of a write whose response was lost. Account, root and operation fences reject late
client responses. Draft intent remains local and distinct from observation.

For a new ordinary canonical input with a validated typed message intent and no
explicit verifier override, admission freezes the current root session preference
on the same transaction and records server-only capture provenance. Present
invalid persisted values are rejected; valid legacy booleans and trimmed on/off
values normalize without changing Memory policy. Explicit input, hidden controls
and legacy/direct calls retain their contracts. Exact command retry reuses the
original frozen input rather than recapturing a newer preference; observation
alone must not create a new retry identity. No per-Send frontend GET gate, new
preference store, automatic POST replay, schema or permission grant is added.

## Snapshot reference query indexes

Schema 74 adds two separate, non-unique indexes on
`artifact_version_provenance.lineage_snapshot_hash` and `env_snapshot_hash`.
Snapshot cleanup can look up either reference without repeatedly scanning all
provenance rows. Multiple artifact versions may still reference the same
snapshot; NULL references and the existing age/reference predicates are
unchanged. Cleanup retains its single transaction, active-work guards, and
post-commit blob checks. The indexes reduce eligibility-scan cost, but do not
place a hard duration bound on the whole sweep.

The additive migration creates both indexes and its checksum-bound journal
entry in one transaction. Existing databases follow the ordinary verified
pre-upgrade backup path; fresh databases receive the same indexes through the
canonical migration registry. Index creation needs time and additional disk
space proportional to existing provenance, and future provenance writes
maintain both indexes.

This upgrade is forward-only: schema-73 binaries refuse a schema-74 database.
A binary rollback requires the verified pre-upgrade backup and the runbook's
state-rollback procedure. Do not remove indexes or journal rows to bypass that
version guard; restoring a backup also discards subsequent workspace changes.

## Project identity retention

Schema 73 keeps successful project IDs in `project_id_history` in the same
workspace SQLite database. Existing projects are seeded during the additive
migration. Insert triggers reserve an ID in the project's transaction and
reject reuse even after project deletion, including SQL conflict modes and
outbox mutations; project IDs cannot be renamed. Failed statements, rolled-back
transactions and failed directory preparation do not reserve the ID. Names and
paths of an existing project remain editable through their usual authority.

A deleted project is not a reusable slot: create a new project with a new UUID
instead of sending the old ID again. The history contains IDs only, not project
contents or owner details. Project deletion and lifecycle pruning do not remove
these identity reservations or authorize deletion of retained workspace files.
For projects deleted before this ledger existed, no deleted-row history can be
reconstructed. New managed CREATE must reject a pre-existing managed directory;
only an existing project's authorized UPDATE may reuse its own directory.

The migration does not move or delete projects or their workspaces. It is
forward-only: schema-72 binaries refuse a schema-73 database. A binary rollback
requires the existing verified pre-upgrade database backup and the runbook's
state-rollback procedure, not deletion of the identity table or migration row.
Restoring an older backup loses later identity reservations; retain current
state for diagnosis and do not treat that restore as permission to reuse IDs or
adopt retained directories.

项目 ID 表示不可复用的实体身份。成功创建后，即使删除项目，也必须使用新的 UUID
创建下一项目；失败或回滚的创建仍可重试。身份记录与项目共用同一 SQLite 事务，只保存
ID，不新增所有者或内容存储。升级不移动、删除项目或工作目录。旧版本回滚须按运维手册
使用已验证的升级前备份；不得通过删除身份记录或迁移日志回退权限边界。

Historical runtime reset and deployment remain integration-controller actions.
The controller must stop the service, verify the resolved data root, preserve
configuration and credentials as directed, remove the approved runtime-history
inventory, start from the integrated revision, and verify fresh SQLite creation,
foreign-key integrity, API health, browser navigation, task execution, restart,
and a post-restart lifecycle sweep. Source files are never part of that reset.

## Preparation failure presentation

The trusted runner preparation boundary attaches a private, cause-preserving
identity only to otherwise generic preparation errors. The public terminal
reason is `runner_history_preparation_failed` for the trusted history replay,
history preparation and context replay stages, or `runner_preparation_failed`
for other preparation stages. Stage identity is not inferred from diagnostic
text; a history-preparation failure does not prove compaction occurred.

Both reasons use fixed localized explanations through the existing settlement,
live/history sanitizer and task-status/detail surfaces. Private errors, paths
and credentials are not interpolated into that copy. Existing cancellation,
deadline, correction, provider and timeout priorities remain unchanged; these
identities grant no new retry, automatic recovery or permission. Older terminal
records are not reclassified or backfilled.

## Context usage projection

The composer reads `GET /api/conversations/{id}/context-usage` through the
conversation's existing ownership check. It returns either an explicit
`unavailable` status or a numeric latest main-agent request observation and its
bounded history (up to128 retained requests, with explicit recorded/latest-only
coverage). The records are kept in task-scoped `runtime-state.sqlite`; task/project
deletion removes it with other operational state. It contains no prompt,
message, tool argument, response body, credential, or endpoint URL. Earlier
conversations have no retroactive record until a new model request runs.

`source=provider` means sufficient provider evidence supports the completed
context total. It does not establish that every individual counter was
reported: for example, a reported total may coexist with estimated output.
`source=estimated` means local estimates contribute to the total or the request
did not complete.
Provider usage is per request, never the cumulative task billing counter.
Input rows estimate five mutually exclusive parts of the actual request:
system instructions, tools and subagents, conversation messages, connectors
and MCP, and Skills. Attribution follows runtime-owned provenance, not
percentages inferred from tool names or user-supplied text. The numeric record
does not retain the underlying content. Image/audio/document token costs are
not inferred from bytes.

The card preserves these request-specific input estimates without scaling them
to a provider total, and includes the latest projected output under conversation
messages. Category values are estimates, not independently metered provider
counters; their sum can differ from the settled total. Display-window ratios
do not turn either value into a verified model-capacity measurement.
Values above 100% remain visible, while the segmented bar normalizes to its
available width so no category is cut off. A tooltip exposes the measurement,
attribution and budget sources without adding diagnostic rows to the card.

An optional `callUsage` on a successful main-model response retains the existing
`agentruntime.ModelUsage` snake-case wire fields. Its `reported` flags preserve
explicit zero versus missing counters; `invalid_fields` preserves current
invalidity independently of historical `warnings`. Omitted zero cache fields
remain valid typed zero when their reported flag is true. Missing `reported`
means presence was not recorded, not that nonzero legacy counters are explicit
provider evidence. `cache_tokens_additive` distinguishes additional cache input
from overlapping cached input; the selected-request detail does not add cache
again or feed these counters back into occupancy/compaction.

The response's reported flags are copied with its counters at finish. An
unrepresentable negative or above-JavaScript-safe counter suppresses only this
optional detail with a fixed diagnostic; it does not poison the existing
context receipt or retry the successful model task. Persisted malformed optional
evidence is rejected by the existing read/decoder error path. Old, failed,
unfinished and preflight observations do not invent completed usage details.
In particular, context-pressure observations can precede provider dispatch:
history sample counts are not a complete physical-call ledger or Turns. No
message/branch association is inferred from runner attempts or timestamps.

The limit is a configured or model-profile context budget when known, not an
independently verified capacity. Unknown capacity stays unknown in stored
telemetry; the UI may use its labeled default1M display budget. Earlier three-category records
remain stored but report unavailable until the next request supplies the
five-category projection. A presentation-only translation
may be billed in provider audits, but cannot replace the agent request shown
in the composer. Missing telemetry and read failures have separate UI states;
the client bounds each fetch and offers retry.
