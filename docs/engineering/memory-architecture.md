# Memory architecture

X-Science implements one workspace memory lifecycle. It does not maintain a parallel session-summary memory, file mirror, or runtime-KV memory ledger.

## Authorities

| Concern | Authority |
| --- | --- |
| Runtime tuning | `internal/memoryconfig` and `internal/memorypolicy` |
| User/project/session enablement | workspace user settings, project auto-recall setting, exact session `memory_mode` |
| Automatic extraction preference | user-scoped control-plane setting `memory.autoExtractionEnabled`, inheriting the deployment capability default |
| Durable facts and categories | workspace SQLite memory tables |
| Agent fact mutations | claimed `write_memory` transcript receipt |
| Category settings writes | workspace category transaction and canonical mutation result ledger |
| Automatic recall | `Store.RecallMemories` |
| Explicit search | `Store.SearchMemories` over the authenticated user's full pool |
| Prompt rendering | `internal/memoryprompt` |
| Post-completion extraction | `internal/memoryextract` plus `memoryExtractionRuntime`; one workspace atomic outcome receipt |
| Logical-task recall stability | durable runner task memory snapshot |
| Conversation continuity | transcript replay and compaction, not a second memory store |

## Turn lifecycle

1. Resolve the authenticated user, project, root frame and source frame from persisted session/frame state.
2. Apply the user, project and exact `memory_mode` gates. Recall and manual management use the master setting; post-completion extraction additionally uses the independent automatic-memory preference.
3. Render workspace memory rules and bounded profile/category facts into a system message.
4. Rank automatic recall with BM25/Jaccard reciprocal-rank fusion, project boost, staleness decay, cross-project quotas and frame isolation.
5. Inject recall as a harness-owned user block immediately before the real user message. Persist served IDs so retries and child tasks do not repeatedly surface the same facts.
6. Seal that context to the logical task so a lease recovery cannot drift to newly written facts.
7. After a durable completed root turn, run one detached, drainable extraction task. Capture its exact completed source, project only that source, honor compaction cursors and suppress recalled-text feedback. Prepare classification and literal repair without a write transaction, then commit allowed mutations, the extraction cursor and the original outcome receipt together.

## Completed-extraction commit and replay

`Service.Prepare` produces values, not persisted effects. `Store.CommitMemoryExtraction` is the only automatic-extraction mutation owner. It uses the existing workspace/transcript SQLite immediate transaction to revalidate the captured completed source, owned root/frame incarnations, persisted memory policy and expected cursor before applying the prepared batch. A canonical source retains stream epoch, branch generation, event/publication coordinates, attempt and consumed input revision. A legacy source retains its strict completed-prefix digest and holds the journal replacement guard only across the short commit.

Accepted appends, snapshot-CAS successors, removals, cursor progression and the immutable schema81 receipt commit atomically. A SQL, cursor or receipt failure rolls them all back; a later retry does not infer success from a fact's existence. Rejected/flagged operations retain their diagnostics. A stale fact snapshot is skipped rather than overwriting or deleting a newer edit. No model, classifier, repair callback or network operation holds the write transaction.

Receipt replay returns the original IDs, cursor outcome and diagnostics before model/cadence/forked-request gates, including after process restart or reply loss. The same completed source with a conflicting input or explicit mutation plan cannot acquire a second write. Later branch changes or memory opt-out do not revoke already committed profile/project facts or turn receipt readback into a fresh write grant; root incarnation and owner checks still apply. This receipt is not an interactive `write_memory` invocation, runtime-KV ledger, new scheduler, or guarantee that every historical completion is enqueued after downtime.

## Isolation and safety

- `memory_mode: "off"` disables tools, prompt recall and extraction. Other spellings and non-string values are not silently normalized into policy decisions.
- Turning off automatic memory stops only post-completion extraction. It does not hide existing memories or create a parallel recall path.
- Project memory disablement suppresses automatic recall and extraction; explicit tools remain available when user/session policy permits.
- Frame facts are visible only to the trusted root frame and are deleted with that conversation.
- Explicit search can find another project owned by the same authenticated user, using the explicit-search contract; automatic recall admits cross-project rows only through bounded quotas.
- User-authored facts cannot be replaced or removed by agents or extractors.
- Extractor successors use compare-and-swap supersession; stale model output cannot overwrite a newer fact.
- Prompt-injection classification, literal preservation, secret redaction, hostile-memory sanitization and owner/scope validation are enforced before persistence or rendering.

## Category settings writes and response loss

The category editor has one document/account-scoped draft and save owner.
Settings navigation disposes a view, not its draft or in-flight operation.
Only the current visible page/modal lease may edit or deliver a result.

Category POST/PUT accept the existing optional `Idempotency-Key` header and
server alias. A logical save freezes its key and original typed payload.
Authenticated owner, key, operation kind and exact request hash identify a
canonical `mutation_result_ledger` entry committed in the same transaction as
the category change. The result retains only the category ID. Replay reads
that owner's current category without applying the old mutation again; an
intervening edit is preserved and a deleted category is never recreated.
Legacy calls without a client key retain independent-operation semantics.

Normal success retains the existing category JSON and HTTP201 for POST or HTTP200 for PUT.
Category errors retain `detail` and may include these machine-readable codes:

| Code | Meaning |
| --- | --- |
| `MEMORY_CATEGORY_WRITE_REJECTED` | An authoritative rejection permits correcting the draft. |
| `MEMORY_CATEGORY_OUTCOME_UNKNOWN` | HTTP503: preserve the original key/payload and retry that operation explicitly. |
| `MEMORY_CATEGORY_COMMITTED_RESULT_MISSING` | HTTP410: the receipt proves a prior commit, but the category is now gone; acknowledge with a warning and attempt current readback. |
| `MEMORY_CATEGORY_IDEMPOTENCY_CONFLICT` | HTTP409: the original key and request do not agree; do not generate a replacement key automatically. |

A network, response-body, decoding, database or unclassified HTTP error does
not prove that a write failed before commit. Uncertain saves keep fields frozen
while allowing a bounded, explicit retry of the original save. Closing the editor
hides it without discarding its operation. Its resume action remains available
even when the new-category limit has been reached. Known acknowledgements use
the existing current-view refresh/notification path and never re-submit because
that readback failed. Account reset invalidates old callbacks; it does not undo
an earlier server commit. Recovery of a lost client UUID across refresh/logout
is outside this document-local editor contract.

Memory metadata requests have a 30,000 ms default total fetch/body wait budget,
with a finite positive timer-range override and real abort/listener cleanup.
This transport budget does not limit research tasks, model turns or jobs.
Other Memory writes gain finite transport waiting; durable keyed replay in
this contract covers category Create/Update only. There is no automatic write
retry, additional journal, receipt endpoint or parallel memory store.

## Removed competing path

The legacy `autoMemoryEnabled` session-summary path, runtime-KV `memory`/`memory-schedule` namespaces, generated Markdown mirror, schedule pruning, token thresholds and duplicate provider call were removed. Durable transcript compaction remains the sole conversation-continuity mechanism; workspace memory remains the sole cross-session fact mechanism.

## Focused verification

Choose exact named regressions for the changed behavior from the affected surface below; this inventory is not a requirement to rerun every listed package or unrelated module:

- `go test ./internal/memoryconfig ./internal/memorypolicy ./internal/memoryclassifier ./internal/memoryprompt ./internal/memorytools ./internal/memoryextract`
- `go test ./internal/persistence/workspace -run 'Memory|WorkspaceExtraction|WorkspaceExtractor'`
- `go test ./internal/persistence/transcript -run Memory`
- `go test ./internal/server -run 'Memory|Workspace|RunnerTaskMemory|SessionRunnerModulesBindOnePhaseMachine'`
- `vitest run --project node tests/unit/xscience/xScienceMemory.test.ts`
- `vitest run --project dom tests/unit/settings/XScienceMemoryManager.dom.test.tsx`

Release acceptance additionally requires a real configured model to complete one root scientific task, create a durable memory, recall it in a fresh session, preserve it across reload/compaction, and prove that an opted-out session and another user cannot receive it.
