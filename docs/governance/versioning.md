# Versioning and releases / 版本与发布

## Product version / 产品版本

[`product-identity.json`](../../product-identity.json) is the only product-version authority.
Code, packages, health responses, and user-visible version text must derive from it.
Do not add a second `VERSION` file or hard-coded product version.

[`product-identity.json`](../../product-identity.json) 是唯一的产品版本权威源。
代码、安装包、健康接口和界面版本必须由它派生，不再增加第二份 `VERSION` 文件或硬编码版本。

## One increment per merged PR / 每次 PR 合并递增

Starting from the current main version, every successfully merged PR advances
the product counter exactly once. The PR type, title and number of internal
commits do not change the increment. Unmerged/closed PRs, failed merges, CI
reruns and repeated preparation do not consume a version. Historical versions
are not renumbered.

The canonical shape is `MAJOR.MINOR.PATCH`: `MAJOR >= 0`, `0 <= MINOR < 10`,
`0 <= PATCH < 100`, without leading zeroes, prerelease or build suffixes.
Increment PATCH by one; at 100 reset it to zero and increment MINOR; at 10
reset MINOR to zero and increment MAJOR. Examples:

| Current / 当前 | Next merged PR / 下一次合并 |
| --- | --- |
| `v0.1.2` | `v0.1.3` |
| `v0.1.99` | `v0.2.0` |
| `v0.9.99` | `v1.0.0` |

This is a per-PR numeric counter, not Semantic Versioning's compatibility
meaning. Compatibility, API, persistence schema and dependency changes still
require their own review; their versions must not be overwritten by this counter.

从当前 main 版本开始，每个成功合并的 PR 只递增一次，包括功能、修复、文档和维护
PR；一个 PR 内有多少提交都不影响计数。未合并、关闭、合并失败、重跑 CI、重复准备
均不占用版本号，不追溯重编号。补丁号满 100、次版本号满 10 时进位。
这是按 PR 计数的版本规则，不表示语义化版本的兼容性承诺；API、数据库和依赖版本保持独立。

### Prepare before review and merge / 合并前自动准备

Prepare metadata on the clean, named task branch before reviewing its final head:

```sh
python3 -B scripts/packaging/prepare_pr_version.py \
  --repo /absolute/task/worktree --base EXACT_CURRENT_MAIN --head EXACT_TASK_HEAD
# Review the local plan, then apply the same exact binding:
python3 -B scripts/packaging/prepare_pr_version.py \
  --repo /absolute/task/worktree --base EXACT_CURRENT_MAIN --head EXACT_TASK_HEAD --apply
```

The tool computes the next version from main, not from an already-prepared
candidate. It changes only root authority, registered JSON version projections,
and their three derived frontend provenance outputs. It validates fresh baseline
bytes using the reviewed tooling auditor; candidate audit code is not executed.
It refuses main/detached/dirty worktrees, stale heads, unrelated ancestry,
unexpected version edits and unsafe paths. It does not commit, push or merge.
Commit the generated metadata on the task branch and review/test that new exact
head through the existing controller. Do not attach old gate evidence to it.

`pr_version_gate.py` checks the exact main-to-candidate Git objects in the
existing required PR policy job, and the exact previous-main-to-actual-main
objects in the existing main integration job. The controller also supplies both
SHA bindings to its existing CI-contract command. Root shape and all projections
remain covered by the identity gate. Only explicit initial main creation has no
previous PR to count; a normal PR cannot use that exception.

Merge PRs serially with up-to-date main protection. If main advances, update
the task branch normally, stop on conflicts, prepare the next version against
that new main and review/test the new head. Never silently overwrite a competing
implementation or force-update main. The version therefore lands atomically
with its successful PR, not through a later bot commit or separate version PR.
No new GitHub App, token, secret, write permission, tag or release is needed.
The superseded Release Please workflow and secondary bookkeeping are removed;
no account credentials or existing published releases are deleted.

版本字段在任务分支准备并随 PR 一起进入 main。自动准备不提交、不推送、不合并；
生成后的精确 head 必须重新审阅并通过原有门禁。重复准备不再次加号；main 前进后重新
按新基线准备，冲突必须先解决，不能靠硬覆盖通过。PR 和 main 校验都只读，不增加凭据或权限。
正式发布仍是单独授权流程，普通 PR 不因此运行完整发布矩阵。

## Release promotion / 正式发布

- Use the product's canonical `MAJOR.MINOR.PATCH` counter for release names.
  A counter increment is not tag or release authorization.
- Tracked source defines only the static [release policy](release-policy.json);
  it can never grant current release or tag authorization.
- Build release candidates once in the full quality workflow. Bind the exact
  source commit/tree and every artifact name, size, and SHA-256 in
  `RELEASE_CANDIDATE.json`.
- Create an annotated tag named `vMAJOR.MINOR.PATCH` only after mandatory
  gates pass and an external signed authorization receipt matches the exact
  candidate manifest and artifact set.
- Publish one GitHub Release from that exact tag; generated release notes are configured in [`.github/release.yml`](../../.github/release.yml).
- Promote those exact candidate bytes without rebuilding. Create a draft,
  attach every asset, then publish only when GitHub Release immutability is
  verified enabled.
- Bind development evidence to a commit SHA. A branch, workflow artifact,
  candidate manifest, or build output is not a release.

- 正式发布名使用产品 `MAJOR.MINOR.PATCH` 计数值；版本递增本身不授予 Tag 或发布权限。
- 源码只保存静态 [发布策略](release-policy.json)，不能授予当前发布或 Tag 权限。
- 完整质量工作流只构建一次候选制品，并由 `RELEASE_CANDIDATE.json`
  绑定源码提交/tree 及每个文件的名称、大小和 SHA-256。
- 只有强制门禁通过，且仓库外签名授权收据与精确候选清单/制品集一致后，
  才创建 `vMAJOR.MINOR.PATCH` 注释标签。
- GitHub Release 必须来自该精确标签；自动发布说明由 [`.github/release.yml`](../../.github/release.yml) 管理。
- 发布只晋级这些字节，不重新构建；先创建 Draft、附加全部资产，再在确认
  GitHub Release immutability 已启用后发布。
- 开发证据绑定提交 SHA；分支、Actions 产物、候选清单或普通构建结果都不是正式发布。
- Installers impose no fixed archive-size ceiling. Archives must still pass safe-path, regular-file, checksum, release-manifest, and product-identity checks; operators must provide sufficient disk space.
- 安装器不设置固定包体积上限；包仍必须通过安全路径、普通文件类型、校验和、发布清单与产品身份验证，并由操作者保证目标磁盘空间充足。

## Package publication / 分发包发布

The `release: published` event triggers
[`.github/workflows/packages.yml`](../../.github/workflows/packages.yml).
It checks the immutable release, exact source revision, successful full-quality
run and every candidate digest before packaging the exact Linux/Windows archives
as an OCI distribution bundle. Binaries and Web assets are not rebuilt.
ORAS performs a local push/pull roundtrip and a digest-bound remote pull after
publication; every filename and checksum must match the original release.

Only the publishing job receives `packages: write`; normal PR/main CI stays
read-only. Publication uses the short-lived `GITHUB_TOKEN`, not a personal token.
The OCI source annotation links the package to this repository. The transport
action is pinned to a reviewed commit and the ORAS CLI has an explicit version.

Use `ghcr.io/victor-xu-1/synon-biomed:vMAJOR.MINOR.PATCH` or a digest, not a
moving `latest` tag. An existing version is never overwritten. A failed job
before push can be rerun; after any partial push, inspect the package first.
Publishing a release through automation must account for GitHub's rule that
events created using `GITHUB_TOKEN` do not trigger another workflow. The release
operator therefore publishes through an explicitly authorized maintainer session.

For the first package, check its visibility and repository linkage in GitHub
Packages settings. GitHub may initially create it as private even for a public
source repository. A release and a package are separate delivery results:
verify an anonymous pull before announcing a public package.

## Paths and compatibility / 路径与兼容

Current source and documentation paths use stable responsibility-based names without a product-version suffix.
Versioned names are allowed only where the version is part of an external contract or immutable distribution identity:

- schema, migration, and compatibility boundaries under `internal/migration`, `internal/compat`, and `docs/compatibility`;
- third-party dependency versions and lock files;
- immutable release archives and installation directories.

当前源码和文档使用稳定、按职责命名且不带产品版本后缀的路径。只有版本属于外部契约或不可变分发身份时才保留版本名：

- `internal/migration`、`internal/compat`、`docs/compatibility` 中的模式、迁移和兼容边界；
- 第三方依赖版本与锁文件；
- 不可变发布包和安装目录。

Historical evidence is moved, not rewritten. Its recorded legacy paths and versions remain factual history.
历史证据只归档、不改写，其中记录的旧路径和版本仍作为事实保留。
