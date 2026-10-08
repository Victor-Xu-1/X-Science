# Pull-request verification scope

`scripts/quality/pr_fast_scope.py` selects changed Go packages and their production and test import consumers using the real Go package graph. Version and checksum changes select consumers of the changed module identities, including indirect external dependencies and imports used only by tests. Module, toolchain, replacement or workspace authority changes and unknown package-external inputs retain conservative whole-graph coverage. A failed graph or manifest lookup fails the check; it never becomes an empty scope.

Verification-only scripts, workflow files and static governance inputs can declare exact ownership in `scripts/quality/verification_scope.json`. Each group lists its files, mandatory executable checks, and whether changes also require the frontend provenance, test, and build gate. The selector executes the declared checks and propagates any failure before running the Go scope. A Go package's embedded input or testdata ownership always takes precedence over a declaration. Wildcards, Go source and module manifests cannot be declared verification-only. Workflow and Action-pin changes run the CI contract tests, not unrelated product tests.

Frontend migration audit scripts belong to the frontend provenance group. Their changes run the two audit regression suites and the existing complete frontend gate; they do not by themselves trigger unrelated Go tests. Scope selection changes run the real Git/Go graph and verification-contract regressions. Unregistered scripts still select the conservative Go graph, so adding a declaration requires review of its actual consumers.

Run the selector regressions with:

```sh
python3 -B -m unittest scripts.quality.test_pr_fast_scope scripts.quality.test_pr_dependency_scope scripts.quality.test_pr_test_partition scripts.quality.test_verification_scope
```

Run an exact change's checks with:

```sh
python3 -B scripts/quality/pr_fast_scope.py --base BASE_SHA --head HEAD_SHA --log-dir /absolute/external/evidence
python3 -B scripts/quality/pr_fast_scope.py --frontend --base BASE_SHA --head HEAD_SHA
```

Use immutable full commit IDs and keep evidence outside the source checkout.

An independently proved `product-identity.json` counter-only change does not
broaden a mixed frontend/dependency PR to every Go package. The identity matrix
must be unchanged and the complete JSON must differ only by one valid version
step. This exemption applies to that file alone: mixed lockfile, dependency,
auditor and runtime changes retain their existing checks. Missing proof, a name/
schema edit or unknown input keeps conservative coverage. Version/projection,
fresh provenance and protected security checks still run; this is scope selection,
not acceptance or a failure waiver.

Frontend NOTICE updates are declared static license verification inputs: their
exact paths run the license inventory/binding checks. Embedded Go ownership
still takes precedence, and unregistered notice/runtime inputs stay conservative.
Version preparation changes only the lock digest in an already-current notice,
preserving upstream terms byte-for-byte; a stale or duplicate binding fails.
The pure version-metadata selector proves the same exact NOTICE delta. Dependency
version/index/license changes must be reviewed and refreshed before preparation.

PR and main checks derive one to four bounded partitions with `--matrix`. Each job
passes its `--shard-index` and `--shard-count` to the same selector. Every discovered
test is assigned exactly once; subtests stay with their parent, and packages without
tests are still compiled once. Each partition retains its own plan, event stream and
completeness summary. The required aggregate waits for every partition and propagates
failures; branch protection is unchanged. Without partition flags, local invocation
runs the complete affected scope. Full runtime, race and release qualification
remain in the scheduled/manual full workflow and are not implied by the scoped gate.
