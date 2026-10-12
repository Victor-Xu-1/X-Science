# Runtime data lifecycle

X-Science has one task-history authority and one bounded operational-state
store:

- `workspace/synonbiomed-v1.1.sqlite` owns projects, frames, canonical
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
| Resolved queued input | 7 days | Queued or delivering input remains |
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

## Context usage projection

The composer reads `GET /api/conversations/{id}/context-usage` through the
conversation's existing ownership check. It returns either an explicit
`unavailable` status or one numeric record for the latest main-agent model
request. The record is kept in task-scoped `runtime-state.sqlite`; task/project
deletion removes it with other operational state. It contains no prompt,
message, tool argument, response body, credential, or endpoint URL. Earlier
conversations have no retroactive record until a new model request runs.

`source=provider` means the completed request's usage came from its provider
response; `source=estimated` means a local text/tool-schema estimate was used
because no usage counters were returned or the request did not complete.
Provider usage is per request, never the cumulative task billing counter.
Input rows estimate five mutually exclusive parts of the actual request:
system instructions, tools and subagents, conversation messages, connectors
and MCP, and Skills. Attribution follows runtime-owned provenance, not
percentages inferred from tool names or user-supplied text. The numeric record
does not retain the underlying content. Image/audio/document token costs are
not inferred from bytes.

The card apportions the reported input total using these request-specific
estimates and includes the latest response in conversation messages. Its five
displayed token amounts sum to the latest request's total; the category values
are still estimates, not independently metered provider counters. Both the
large percentage and the category percentages use the same context budget.
Values above 100% remain visible, while the segmented bar normalizes to its
available width so no category is cut off. A tooltip exposes the measurement,
attribution and budget sources without adding diagnostic rows to the card.

The limit is the runner's configured context budget or its documented default,
not an independently verified model capacity. Earlier three-category records
remain stored but report unavailable until the next request supplies the
five-category projection. A presentation-only translation
may be billed in provider audits, but cannot replace the agent request shown
in the composer. Missing telemetry and read failures have separate UI states;
the client bounds each fetch and offers retry.
