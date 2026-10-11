# Scientific kernel execution contract

The Go Kernel Manager owns session identity, generation, authorization,
execution deadlines, cancellation, process containment and artifact accounting.
The Python worker is an interpreter boundary, not another scheduler.

## Modules and protocol

The single entrypoint is `assets/optional/kernels/kernel_worker.py`.
Modules under `synon_biomed_runtime` separate transport, output capture,
compilation, execution and process hardening. Local and provider sessions use
this same entrypoint. There is no source-string patcher or alternate worker.

Requests remain newline-delimited JSON objects containing `id`, `code`,
`origin`, `tool_name`, `workspace_dir`, optional `working_dir`,
`host_enabled` and `fresh`. The protocol is specific to this application,
not JSON-RPC or a notebook protocol.

- Successful admission emits `execution_started` before executing user code.
- Syntax and unavailable unconditional import-prefix checks return
  `code_preflight_required`, `executed=false`, without that acknowledgement.
- Standard Python statements share a namespace and working directory until the
  host restarts the session. A fresh session is a separate host-owned process.
- Stdout chunks and exactly one terminal response retain the request identity.
  Terminal fields are `stdout`, `stderr`, `error`, `interrupted`,
  `preflight`, `trace` and `usage`.
- Python, native-library and child-process output use cell capture descriptors,
  separate from the protocol. Guest stdin is EOF, not the command stream.
- Cancellation before acknowledgement remains host-owned pending state.
  During execution SIGINT interrupts the cell while preserving earlier state.
  Large protocol writes use buffered full writes so a repeated signal cannot
  silently discard the tail of a terminal response.

The existing live presentation boundary is 10 MiB with a truncation notice.
Each terminal output stream is bounded to 16 MiB of JSON-encoded text, with an
explicit notice when truncated; both streams and diagnostics fit the 48 MiB
host frame limit. Scientific datasets should be saved as artifacts, not dumped
into a console stream.

Provider sessions retain their output-scrubbing and between-cell idle callbacks.
Host RPC is implemented by the existing `synon_host_bridge` adapter; Go binds
each call to the active cell and its allowed methods. Interpreter helpers and
audit hooks do not replace the OS sandbox or server authorization.

## Durable detached execution recovery

Runner lease renewal retries transient store contention only inside the remaining
owned grant, rather than the short four-attempt preparation budget. The retry
requires a deadline before making a write, honors parent cancellation, and returns
permanent errors immediately. Durable expiry and immutable claim fences are still
checked by the store after acquiring its lock; retrying does not create or extend
authority itself. Ordinary preparation writes retain their existing attempt cap.

On the supported detached backend, accepting physical work reserves one observer
before the durable execution becomes visible to recovery. Foreground waiting and
background supervision share that reservation; reconnecting does not start a
second execution or replace its operation identity.

The immutable result receipt is authoritative even after the executor socket
disappears. Settlement retains execution, operation, backend generation, request
and confinement bindings and verifies the existing result/spool digests. A
foreground observation, control-renewal or persistence failure is not a terminal
calculation result: accepted work remains under the existing recovery authority.
Explicit identity and runner-claim rejection is not converted into authorization.

Proven executor loss without a terminal receipt settles as `outcome_unknown`,
including during the original service boot. A live process is not declared dead
because its socket or heartbeat is temporarily unavailable. The detached-only
transition preserves current owner/frame incarnations and any live source runner;
cancelled tasks are not requeued. It does not retry the physical calculation.

An interrupted evidence-loss settlement remains eligible until its operation is
materialized. Operation state, result, terminal event and notification use the
existing atomic settlement path and idempotency identities. Recovery scans use
an immutable execution-ID cursor and wait after a complete scan, so a full page
of active work cannot permanently hide later results.

Focused lifecycle regressions use temporary persistence and controlled process
identity. They establish these recovery contracts, not a guarantee of uninterrupted
24-hour hardware availability or scientific validity.

Recovery terminalization is fenced to the active stream, input revision, exact
expired attempt and current non-resumable checkpoint in one transaction. An
already-failed Frame can still receive its missing runner receipt, without
replacing the original failure description, completion time or context data.
Newer claims, input, checkpoints and live leases remain outside that settlement.

## Progress and transfer supervision

Progress observation waits for the authoritative execution result, not a total
calculation duration cap. An execution callback panic or exit without a result
must produce an error rather than terminate the service or leave its observer
running forever. Ordinary execution errors and successful results remain intact.

An HTTP idle deadline is committed separately from received bytes. Read completion
invalidates a queued close; a committed close finishes before the next read starts.
Received bytes and eventual EOF cannot erase a deadline that committed before
read completion. Bytes remain available, with both the causal idle timeout and
the underlying read outcome retained; a read that wins the race is not closed
by a late callback. Transport retry, authorization and persistence remain with
their existing callers.

## Observation effects

Scientific implementation selection does not itself authorize or prohibit a
diagnostic expression. `internal/executionprep` can positively classify a small
observation domain using complete native AST coverage. Empty requirements or an
empty unresolved list are never proof of observation. Scripts, unknown calls,
dynamic lookup, unbounded syntax and uncovered operations retain the scientific
selection requirement.

| Language | Covered observation domain | Actual binding obligation |
| --- | --- | --- |
| Python | Literal expressions and builtin output with literal operands | Exact builtin identity, unchanged implicit output/trace bindings, and host-owned interpreter provenance |
| R | Atomic literals and zero-argument directory/process/system observations | Captured base bindings and implicit output identity, without evaluating active bindings or promises |
| Bash | Literal directory/output operations and bounded formatting | The canonical Bash envelope establishes profile-free native startup and rejects inherited callable shadows |
| PowerShell | Literal expressions and native location/output cmdlets with literal operands | The native AST and effective cmdlet identity are checked in the execution process using system modules |

The plan binds the exact source digest, language and registered operation
capabilities. It travels in a private host context and optional internal worker
request fields `observation` and `observation_code_sha256`, never in a model tool
schema. Bash binds its original command separately from its generated canonical
interpreter envelope. The manager checks the existing session identity and
generation and freezes the plan before queueing it. Source/binding checks run
under the same execution lock as dispatch.

Once unproved code has been dispatched, the manager retains a conservative
provenance mark outside the mutable Python/R namespace. A later diagnostic cannot
clear that mark or rely on a guest-local claim of clean bindings. Rejection does
not reset the interpreter, select an implementation, load its required Skill,
clear capabilities, or grant filesystem/network/host permissions. Already
authorized scientific execution continues through the existing ordinary path.
An observation classification is not a sandbox safety claim.

The Bash diagnostic invocation uses the existing envelope with `--noprofile
--norc -p -c`: login profiles, `BASH_ENV`, imported shell functions and inherited
shell options cannot run before its source. It does not create a persistent
shell namespace or replace the Python worker. The worker declines known inherited
callable shadows instead of silently accepting them. Ordinary Bash execution
retains its existing startup behavior. Native PowerShell is required; a missing
runtime or the compatibility implementation cannot discharge native obligations.

`scripts/quality/test_kernel_worker_effects.py` exercises the real worker
protocols, preserved namespaces, prior-cell shadows and non-executing refusal.
Native R checks use `SYNON_TEST_RSCRIPT` to identify an existing interpreter;
the tests do not alter managed runtime metadata. Focused Go tests additionally
cover native ASTs, serialized provenance, source/generation binding and canonical
Bash/PowerShell execution on their supported platforms.

## Process policy

The Linux/amd64 syscall filter is generated from named Linux ABI constants.
It validates the architecture, rejects unsupported syscall ABIs, and denies
process tracing, cross-process memory/fd access, keyrings, asynchronous syscall
submission, and new Unix/netlink sockets. The host's isolated network namespace
and explicit egress relay govern IP transport. The interpreter applies
`PR_SET_DUMPABLE=0` after startup.

Native macOS runtime assets may be installed, but that does not make a kernel
executable. Until a native boundary is verified, the Darwin kernel manager
reports `kernel_confinement_unavailable` and refuses to launch Python, R, Bash,
or provider workers. Requests for kernel egress also fail rather than silently
dropping their domain policy. Removing this refusal requires a real macOS
probe and regression coverage for per-task read/write and protected paths,
direct network denial with approved egress through a host broker, inherited
restrictions for Python/R subprocesses, and termination of descendants that
detach from the initial process group. Cross-compilation alone proves none of
these runtime properties. The separately supervised detached executor and
backend are Linux-only; native macOS kernel confinement alone would not
establish parity for their durable background-execution semantics. The native
in-process manager is a separate path.

On native Windows, the confinement probe also remains unavailable. An
AppContainer launcher has been exercised with a local test executable: the
target starts with a restricted token inside a kill-on-close Job, passes
explicit stdin/stdout/stderr handles, writes only its temporary workspace,
reads a separately granted temporary read-only tree, and cannot read an
ungranted private file or connect directly to a local TCP listener. A
server-owned lease receipt supports revoking temporary ACLs and deleting the
profile after normal exit, cancellation, or a crashed host. The candidate
launcher derives narrow read-only grants for a managed runtime and worker
assets, checks frozen mount identity, grants only a trusted frozen R operation
log for external writes, and starts Python with `-I`. These code and
AppContainer-helper tests do not establish a complete managed Python/R
runtime boundary. The kernel manager still refuses native Windows execution:
protected-path denial, auxiliary provider channels, approved egress, and
actual installed-interpreter startup/recovery lack equivalent native evidence.
No network request is silently allowed or dropped: without a broker, an
approved-egress request fails closed. Managed environment installation and
native kernel execution remain separate readiness facts.

The opt-in Windows interpreter probe sets `SYNON_TEST_WINDOWS_PYTHON_EXE` to an
installed Python generation's executable and `SYNON_TEST_WINDOWS_KERNEL_ASSETS`
to an ordinary drive-rooted copy of the current kernel assets. It runs the
worker with `-I -S` to isolate the process/transport boundary, verifies a
completed cell, denied direct network access, EOF and cancellation, and checks
ACL cleanup. It does not verify the production `-I` startup path: the installed
generation's site startup and Python-created private subdirectories still need
native readiness tests. The candidate now validates frozen single-file mount
identity and R operation-log write scope; their behavior under an installed R
generation still requires native testing.

See the public [Linux seccomp interface](https://www.kernel.org/doc/html/latest/userspace-api/seccomp_filter.html)
and [Python signal semantics](https://docs.python.org/3/library/signal.html).
These describe platform APIs, not an imported execution implementation.

## Integrity and verification

Saved molecule validation uses the integrity-checked managed RDKit interpreter
and `sdf_artifact_validator.py`. Its internal v2 summary counts delimiters,
supplier records, parsed molecules, invalid records and atom-count coverage;
all molecules are parsed with sanitization and strict SDF parsing. A missing
terminal delimiter, zero-atom molecule or invalid tail remains a failure.
The summary does not accumulate per-molecule arrays or impose a total input
byte ceiling. Memory still includes one molecule or SMILES line, so resource
exhaustion is a real validator failure, never evidence of valid data. The
validator asset and host consumer must be updated together; v1 output is not
silently treated as a complete v2 summary.

Save failures preserve the validator's defined data-error code, format and
observed record/parsed/invalid counts. These are diagnostic counts, not physical
line locations. Raw parser stderr and molecule content are not exposed in the
failure detail. Valid results require exit 0; defined invalid-data results
require exit 2. Unexpected exit/result combinations, unknown data-error codes,
and malformed validator responses are runtime validation failures, never
evidence that the user's file needs rewriting. No artifact is published until
the complete file passes the managed validator.

Molecule and saved-Python validators share the owned process-tree boundary and
activity watchdog. Cancellation and sustained absence of CPU, I/O or output
stop the helper; elapsed total validation time alone does not. Python source
goes directly to the interpreter's symbol-table parser, without an additional
whole-file host buffer or duplicate AST parse. Symbol analysis still requires
compiler memory for the source; only its diagnostic name sample is bounded,
with an exact unresolved-name count. Artifact code is never executed by these
validators. A missing or mismatched managed runtime remains unavailable and
must not be bypassed with a host-Python fallback.

`python3 scripts/quality/kernel_manifest.py --write` reproducibly updates the
asset inventory; omit `--write` to verify it. The generated manifest includes
all execution modules and retains component metadata without granting licenses.
Agent definitions use the existing `agent_manifest.py` generator.

`python3 scripts/quality/test_kernel_worker_protocol.py -v` checks actual
interpreter pipes, persistence, Unicode, exceptions, stdin isolation, native
output, interruption, and optional installed plotting support. The targeted
Go kernel tests separately exercise real confinement, host calls, provider
credentials, session identity, cancellation and restart through the manager.
Full application acceptance and distribution licensing are separate gates;
passing these tests is not permission to publish unreviewed third-party assets.

The protocol regression entry supports both direct-file and package-module
invocation without adding the repository root to global PYTHONPATH. Both
entry modes accept named cases, including inherited process-outcome cases.
For example:

    python3 -B scripts/quality/test_kernel_worker_protocol.py WorkerProtocolTests.test_discarded_nonzero_results_are_executed_failures
    python3 -B -m unittest scripts.quality.test_kernel_worker_protocol.WorkerProtocolTests.test_persistent_namespace_and_expression

These fixtures use temporary worker processes and real interpreter pipes;
they do not run a user research task or establish long-duration endurance.
