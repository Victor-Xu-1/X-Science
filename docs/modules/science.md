# Science · 科研执行

本层把“需要什么科学软件和输入”转为受控的执行环境、进程和输出记录。
它不把模型描述当作计算结果，也不保证目录中列出的每个可选引擎在当前机器上可用。

## Find the implementation · 实现入口

| 责任 | 源码入口 |
| --- | --- |
| 环境复用、资源预检与创建入口 | [agent_environment_management.go](../../internal/server/agent_environment_management.go) · [agent_environment_resource_preflight.go](../../internal/server/agent_environment_resource_preflight.go) |
| 不可变环境代际及就绪检查 | [kernel/managed_environment.go](../../internal/kernel/managed_environment.go) · [managed_environment_readiness.go](../../internal/kernel/managed_environment_readiness.go) |
| Python/R/Bash 调用与执行身份 | [server/agent_kernel.go](../../internal/server/agent_kernel.go) · [kernel](../../internal/kernel/) |
| 远程计算与 provider 生命周期辅助 | [compute](../../internal/compute/) |
| 能力、引擎、输入和输出见证的契约 | [sciencecapability/contract.go](../../internal/sciencecapability/contract.go) · [scientific-capabilities.v2.json](../../internal/sciencecapability/scientific-capabilities.v2.json) |
| 安装空间和运行目录用量 | [runtimecontrol](../../internal/runtimecontrol/)；此包不是科学能力目录 |

## Preparation is not a result · 环境就绪不等于任务成功

面向模型的环境流程是预检、复用或创建、按需安装，然后指定环境执行。
已有兼容环境应优先复用；新一代环境通过实际解释器和所需 import 校验后才能激活。
失败或取消不能把 staging 内容标成 ready。详细顺序与恢复规则只由
[Managed scientific execution](../engineering/managed-execution-runtime.md) 维护。

| 观察到的事实 | 可以说明什么 | 不能说明什么 |
| --- | --- | --- |
| 目录列出能力或软件 | 有对应配置或能力定义 | 已完成下载、获得凭据或执行成功 |
| 环境显示 ready | 该环境通过其安装和健康检查 | 某个研究结论已经验证 |
| 实际进程完成并保存输出 | 有一次具体执行记录 | 输入、方法和科学解释必然正确 |
| 能力见证校验通过 | 受信适配器提供了契约要求的身份和产物信息 | 可以跳过科学质量审阅 |

当前 native Windows/macOS 可包含科学运行时安装资产，但 kernel confinement 边界仍是
Linux/WSL。不要把 native 安装成功写成该平台已能执行科学任务。支持矩阵、配置及恢复方式见
[运维手册](../operations-runbook.md)和 [Kernel execution contract](../engineering/kernel-execution-contract.md)。

通用环境预检读取核心 Python/R 已验证的目录、JSON 激活指针及代际身份，
不会把合法的便携式指针当作损坏安装，也不为核心环境写第二份管理标记。
核心 Python 的额外包沿已有不可变派生环境流程安装；原始核心代际不会就地修改。
目录可用、包已安装、引擎具备能力和实际计算完成是不同状态，不能相互替代。

## Focused verification · 验证入口

### Interactive structure calculations · 交互式结构计算

Electrostatic maps, 2D interaction diagrams, and energy minimization use one
request-scoped kernel lifecycle in [structure_preview_kernel.go](../../internal/server/structure_preview_kernel.go).
Completing the parent task does not make a running preview calculation idle.
Request timeout/cancellation and explicit kernel/workspace cleanup still apply.

电性图、2D 相互作用图和能量优化统一由预览请求管理内核生命周期；父任务已完成
不代表正在运行的预览计算空闲。原有请求超时、取消及内核/工作目录清理保持有效。

APBS OpenDX values are transmitted byte-for-byte through lossless gzip/base64.
One map may use the existing 36 MiB aggregate encoded budget; multiple maps
must share that same budget. The 64 MiB per-map / 128 MiB aggregate decoded
limits and grid, atom, identity, alignment, and scientific-report checks remain.
This does not resample grids, round potentials, or fabricate missing ligand bond orders.

APBS OpenDX 通过无损 gzip/base64 逐字节传输。单张图可使用现有 36 MiB 总编码预算，
多张图仍共享该预算；解压后单图 64 MiB、合计 128 MiB 的限制及网格、原子、身份、
对齐和科学回执检查不变。不降低网格精度，不对电势值舍入，也不虚构配体键级。

```sh
go test ./internal/server -run '^TestStructurePreviewKernel|^TestEncodeStructureElectrostaticDX' -count=1
cd frontend && npx vitest run tests/unit/previews/structureElectrostaticMap.test.ts
```

从仓库根目录执行：

```sh
go test ./internal/sciencecapability -count=1
go test ./internal/kernel -run '^TestExecutionPreparation|^TestBashSessionSeparates' -count=1
```

前者检查能力目录与执行见证，后者检查准备阶段和 Bash 会话边界。
涉及安装发布、进程恢复或特定科学引擎时，还需按 managed execution 文档运行相应真实环境检查。
此处不自动下载模型权重、开通付费算力或对正在运行的任务做故障注入。

[模块导航](README.md) · [Runtime](runtime.md) · [Evidence](evidence.md)
