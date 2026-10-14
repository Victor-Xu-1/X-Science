# Repository quality tools

These scripts enforce the current X-Science repository boundaries.
They are verification gates: passing one gate proves only the contract named by
that gate, not complete product acceptance.

## Run affected verification

Normal PR/main frontend gates use Vitest's dependency-related selection against
the exact changed source and test paths, not the global frontend unit suite.
Canonical provenance, type/lint/format, build and the existing single packaged
artifact contract remain. No `passWithNoTests`, retry, target-specific mapping,
or global fallback is used. Unknown shared configuration/dependency ownership
fails clearly until focused coverage is bound; it does not silently run all
tests. Separately authorized real integration/full qualification remains separate.

普通 PR/main 前端门禁按精确变更的源码和测试路径，使用 Vitest 的依赖关系选择
相关测试，不默认执行全量前端单测。保留规范溯源、类型/静态/格式、构建和已有
单一打包制品契约。无测试、未知共享配置/依赖范围不会被伪装成通过或自动全量；
须先明确专项覆盖。真实集成与全量资格验证仍属独立授权流程。

Select the changed tools and their directly related tests. The following whole
quality-tool suite is for explicit comprehensive qualification, not the default
for every PR:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover \
  -s scripts/quality -p 'test_*.py'
```

## Content authorities

Verify the content-addressed bundled Agent manifest:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 scripts/quality/agent_manifest.py \
  --repo . --check
```

Use `--write` only after reviewing Agent metadata or capability changes.

Verify the kernel/compute asset manifest and real interpreter protocol:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 scripts/quality/test_kernel_manifest.py
PYTHONDONTWRITEBYTECODE=1 python3 scripts/quality/kernel_manifest.py
PYTHONDONTWRITEBYTECODE=1 python3 scripts/quality/test_kernel_worker_protocol.py
```

The manifest generator's `--write` mode updates checksums, not redistribution
permissions. The protocol tests use POSIX pipes and signals; an installed
Matplotlib runtime enables the additional real plotting check.

Verify dependency-manager ownership and locked manifests:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 scripts/quality/dependency_authority.py \
  --repo . --spec docs/governance/dependency-authority.json
```

Clean-clone verification prefetches the locked Go modules with at most three
attempts. Transient transport failures are retried with bounded backoff; final
failure remains fatal, and signed module-download URLs are not echoed:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 scripts/quality/go_module_download.py \
  --attempts 3 --timeout-seconds 300
```

The clean-clone gate also installs the pinned MCP Python test requirements into
the user-site of its private external HOME. MCP subprocesses inherit that HOME
through the existing safe environment allowlist, while PATH, the repository,
and the real user Python installation remain unchanged.

Verify the canonical module layout, banned duplicate roots, and growth ceilings:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 scripts/quality/module_topology_gate.py \
  --repo .
```

Verify product identity and every checked-in projection:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 scripts/quality/product_identity_gate.py \
  --repo . --mode candidate
```

`--mode release` deliberately remains blocked after validating the static
release policy, because tracked source cannot grant current release or tag
authorization. The external release controller must validate a signed receipt
against the exact candidate manifest and artifacts.

## Checkout and runtime portability

Reject tracked paths that collide or cannot be checked out on supported
case-insensitive Windows filesystems:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 scripts/quality/portable_paths.py --repo .
```

Run an argv-only command set against an exact clean local clone:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 scripts/quality/clean_clone_gate.py \
  --source . \
  --sha "$(git rev-parse HEAD)" \
  --commands docs/governance/templates/clean-clone-command-set.json \
  --temp-root /absolute/private-temp-root \
  --output /absolute/new-clean-clone-result.json
```

The clean-clone gate never consumes uncommitted files. Use `--validate-only`
to validate a command-set document without cloning or executing it.

After building a candidate binary, probe the real `/health` startup contract in
an isolated temporary runtime:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 scripts/quality/startup_smoke_gate.py \
  --binary ./path/to/xscience --timeout-seconds 30
```

The binary path must be relative to the current directory. The gate selects a
free loopback port, validates the JSON health response, stops the process, and
reports only bounded hashes and byte counts for captured output.

## Read-only repository evidence

Capture a redacted repository snapshot and derive a hygiene report outside the
source tree:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 scripts/quality/repository_state.py snapshot \
  --repo /absolute/repository \
  --label hygiene-audit \
  --output /absolute/private-evidence/snapshot.json

PYTHONDONTWRITEBYTECODE=1 python3 scripts/quality/repository_state.py hygiene-report \
  --snapshot /absolute/private-evidence/snapshot.json \
  --output /absolute/private-evidence/hygiene-report.json
```

Snapshots classify paths and withhold credential or user-data content. They do
not authorize deletion, staging, integration, release, or deployment.

## CI relationship


Pull requests use `.github/workflows/quality-pr.yml`: policy/static checks,
changed Go packages plus their production and test-import dependency closure,
and the frontend scope when frontend files or product identity changed. A Go
dependency version/checksum change selects its actual module consumers, including
external indirection and test-only imports. Toolchain/module/replacement authority
changes or unclassified package-external inputs retain complete-graph coverage.
Declared verification-only workflow changes run mandatory CI contract checks.
Documentation-only changes do not run product tests.
Bare `make` defaults to the existing production build, not `go test ./...`.
CodeQL Go autobuild may invoke `make` to resolve/build dependencies; it must not
silently rerun the global test target before extracting Go sources. Explicit
`make test` is still available for authorized comprehensive testing. The scope
planner recognizes only the exact default-goal addition as a build-entry change;
any build/test recipe or other Makefile change retains normal consumer coverage.
An exact one-step product-version change is classified by Git object content,
not by the names of its seven projection/provenance files. When only the
registered version fields, their corresponding manifest hashes and auditor
pins change, those metadata paths do not expand Go dependency closure or cause
npm installation and the frontend suite/build. The read-only counter and
identity checks plus a fresh clean frontend provenance audit still run. Mixed
PRs retain every implementation path; dependency/script/schema/identity/auditor
implementation changes, incomplete metadata and any uncertain classification
retain normal coverage. Selection never turns a failed provenance check into
success. No target, scientific workflow or task-specific exception is used.
Before frontend dependency installation, the fast path verifies the same
reviewed migration manifest used by full CI. Run it from a clean candidate or
clone; stale provenance fails before expensive frontend checks. Do not delete
unowned local dependencies or build output to force this condition.
Go tests share the existing inventory/evidence executor in one to four bounded,
disjoint partitions and batches of at most 64 top-level tests. All discovered
tests and subtests remain covered; every partition's failure propagates to the
required aggregate. Distinct artifacts retain complete planned/executed inventories.
Shared verification-only checks run once in partition zero; the required
aggregate still depends on that partition succeeding. Invalid partition inputs
fail before they can skip this verification. Other partitions keep all their
assigned Go tests without repeating identical Python/CI checks.

Frontend public bootstrap and service-worker sources have explicit existing
behavior-test owners. Browser/real-backend test harnesses and their runner
configuration are syntax-compiled without evaluating imports or starting real
tasks; this is not browser or scientific acceptance. Renamed harness inputs
route through unique regular-file Git counterparts, retaining actual changes.
Snapshot fixture inputs retain the mandatory content/provenance audit and are
not mislabeled as Vitest modules. Product source changes still run their real
dependency-related unit/component tests and production build; unowned runtime
inputs fail rather than expanding into an unauthorized global suite.
The root npm manifest and lockfile retain locked installation, canonical
dependency/provenance, type/lint/format and product-build checks. They are build
inputs, not documentation and not executable Vitest modules.

The authoritative full workflow is `.github/workflows/quality.yml`. It runs by
manual dispatch and daily at 02:00 in the explicit `Asia/Shanghai` timezone on
the default branch, combining these gates with frontend type/lint/test/build checks,
Go formatting, vet and race tests, source/security audits, clean-copy builds,
and release lifecycle verification. Routine PR review uses the required scoped
checks; comprehensive qualification remains the scheduled/manual full workflow,
not a claim inferred from a fast check. Native Windows release validation first
tests its environment isolation, then performs the complete installed lifecycle.
A promotion of an exact verified revision does not repeat checks merely because
the branch name changed. Changed inputs require fresh applicable evidence.
This split does not waive configured required checks or release qualification.
Non-embedded runtime assets use exact declarations in
`runtime_input_scope.json` to select their Go owners and every production/test
import consumer. Missing declarations or unavailable owners retain conservative
coverage. These declarations cannot exempt runtime tests or replace the scheduled
full release matrix. Installer build inputs retain the same runtime consumers
as the resulting installer, including the asset-integrity and environment tests.
An exact manifest/root declaration expands only that manifest's listed regular
files, not a directory or wildcard. Immutable Git counterparts can route retired
namespace paths to these current owners without dropping changed behavior;
content-addressed Conda counterparts additionally require identical locked
package records. Unknown or ambiguous counterparts retain conservative handling.
The standalone `go_namespace_equivalence.go` checker is an exact verification
input, not a blanket Go exemption: actual Go or embedded-file ownership wins.
Optional scientific-engine test sources have an explicit inert Python syntax
check and retain their declared Go consumers. This does not claim RDKit,
single-cell analysis, scientific-task execution, or native Windows lifecycle
acceptance; those require their own appropriate environment and evidence.
GitHub's scheduled execution can be delayed by runner/platform load. When hosted
execution is unavailable, local evidence must identify the exact revision,
environment, tested scope and missing gates under a distinct local status; it
must not be represented as a completed GitHub-hosted workflow or as full quality
success when only focused checks ran.
The current protected-public controller uses CI-first evidence: it rechecks
exact head/base/tree, scoped CI and security checks, review and the counter,
then normally merges the PR. It does not repeat the same suite on the operator's
machine. Actual-main checks and task/data-safe deployment remain required;
deployment smoke covers the shared baseline and only the directly affected
user interaction. Full release qualification remains a separate boundary.

### Affected Go cost-aware scheduling

PR/main Go partitions compute the same complete assignment independently before
selecting their own index. Global top-level-test LPT orders by descending integer
milliseconds, then full package identity and test name. Placement minimizes the
estimated total load, including a 2200ms startup/package-initialization fee for
each new 64-test process batch, with partition index as the final tie-breaker.
Subtests stay with their parent. Packages with no tests retain SHA256-based
exactly-once compilation. Scope selection, discovery, `-count=1`, the 30-minute
process timeout, failure propagation and partition-zero verification are unchanged.
Required checker failures still stop before Go discovery; after discovery,
duplicate names and incomplete/overlapping assignments fail before execution.

`pr_test_costs.json` is a consumed, sparse scheduling configuration, not a test
allowlist. Its initial 62 entries are medians of successful pass timings from
three compatible runs, retaining only tests at least 5000ms. Unknown/new/renamed
names and tests without pass evidence use a positive 1000ms default. Deleted or
out-of-scope entries never create execution items; unused entries are counted.
The batch fee models mixed Go startup, package initialization and tool overhead,
not a measured compilation-only cost. Raw timings and acceptance reports remain
outside source. Historical package keys are projected to the current module
namespace; the initial projection preserves all 62 unchanged top-level test
names in their verified package counterparts. These remain estimates, not
current measurements. The calibration runs are [PR181](https://github.com/Victor-Xu-1/X-Science/actions/runs/38243297421),
[PR182](https://github.com/Victor-Xu-1/X-Science/actions/runs/38246987592) and
[PR184](https://github.com/Victor-Xu-1/X-Science/actions/runs/38250581962).

The strict v1 profile records Go version, GOOS, GOARCH, race mode and batch size,
observations and an explicit validity window of at most 30 days. Every partition
uses the immutable requested head's commit timestamp as its expiry anchor, never
its runner's current time. Missing, expired, future-observation or incompatible
profiles explicitly fall back to 1000ms per test and 2200ms per batch. Old-SHA
reruns intentionally retain that SHA's time semantics. Invalid schema, duplicate
JSON keys/identities, noninteger/boolean/nonfinite/nonpositive costs, costs over
1800000ms, files over 64KiB, symlinks, traversal and nonregular files fail clearly,
even when the profile would otherwise be stale. Recalibrate only from recent
compatible successful pass evidence; scheduling weights never change eligibility.

Each external `plan.json`, including an empty partition or empty Go scope, binds
base/head, actual checkout HEAD, clean or local-dirty source state, immutable
anchor, environment, profile content SHA256/status, algorithm/schema, canonical
complete inventory SHA256 and full-assignment SHA256. Execution refuses a checkout
HEAD different from `--head`. Each partition validates full union and disjointness
before taking its own assignment. Empty partitions write plans and summaries
through the same executor without spawning Go commands. A local-dirty plan is
local evidence, not an exact clean-head hosted result.

The existing required aggregate validates job success only; it does not download
artifacts or independently validate cross-artifact union. Historical timing
replays predict placement benefits, not actual wall-clock savings or completed
coverage. Check the new exact-head artifacts, actual test-step wall time,
partition spread and batch count before making a performance claim. Full/race
scheduling and release qualification remain unchanged. Rollback is to revert the
scheduler/profile change through a reviewed PR; do not remove tests or assertions.
