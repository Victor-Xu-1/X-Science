# Science · 科研执行

本层把“需要什么科学软件和输入”转为受控的执行环境、进程和输出记录。
它不把模型描述当作计算结果，也不保证目录中列出的每个可选引擎在当前机器上可用。

## PDF preview presentation · PDF 预览方式

The existing authenticated PDF byte reader and PDF.js/react-pdf document own
every supported source. Presentation follows the **loaded** page count, not
the filename or advertised metadata: one page uses the same shared artifact
viewport geometry as images, with bounded raster density, pan, zoom and reset;
multi-page documents keep the existing navigation/reveal reader. Parsing must
finish before either control surface is mounted. Resource generations reject
late parser/page failures from a closed, replaced or retried source.

单页 PDF 沿现有解析器、字节来源与权限链显示，复用图片的视口几何和键盘操作；
拖拽、Ctrl/Meta 滚轮和双击复位仅由单页界面显式接入，不更改原有图片选点行为。
已有 PDF 批注仍可通过选择模式使用；这项扩展不把注释或裁剪称为原生图表证据。
同源重试保留多页缩放和已到达页码，更换来源则重置，不替换后端存储、阅读绑定
或队列快照。原生图表、翻译及实际浏览器的渲染验收有各自的验证边界。

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

## Optional chemical validation · 可选化学验证与产物完整性

保存成功只表示真实文件已按其不可变版本保存，不等于化学结构已经验证。缺少可选的
分子校验能力时，保存结果保留原字节、版本和摘要，并明确返回“校验未执行”警告；
正常交付与恢复交付使用同一分类，在最终答复中追加一次说明，不伪造有效性通过或
重新执行已完成的科研步骤。可信解析器明确报告的无效结构仍须处理；读取失败、
文件缺失、路径/项目身份错误、运行时代际或协议异常和取消不能作为可选警告忽略。

本分子产物完成链使用同一可取消字节流完成解析和实际大小/SHA-256核对；解析器
缺失或提前结束也不能跳过剩余字节。这不代表其他格式或所有历史链路均有相同检查。
恢复或普通续接中，已确认的无效结果只有在真实失败终态回执、原候选及完整不可变
选择集能够准确对应当前版本和摘要时才继续约束交付。该只读重建保留原分支、输入、
删除标记及事件水位，不修改旧日志。未封存、证据不完整或确实更换版本的情况明确
提示历史失败尚未重新验证，不把它当作有效性通过，也不按文件名永久限制新版本。
新版本仍按当前实际检查判断；无法重建旧选择集不触发科研工具重跑。
历史继承还要求当前完整集合采用同一种解析格式，并且原宿主类型化结果中的所有
精确错误码都符合该格式既有的封闭契约。这只保留集合级无效事实，不表示恢复了
每个文件当时的格式，也不把错误归因于某个文件。当前名称无法识别格式、混合格式、
聚合文字、未知或不匹配错误码均保持“历史未重新验证”；重命名不能让成员静默消失。

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

Ligand-surface availability uses the same verified chemistry resolver as the
calculation input. Coordinate-only ligands remain viewable in 3D, while their
unsupported electrostatic action is disabled with a localized explanation;
structures with declared ligand chemistry keep that action available.

APBS OpenDX 通过无损 gzip/base64 逐字节传输。单张图可使用现有 36 MiB 总编码预算，
多张图仍共享该预算；解压后单图 64 MiB、合计 128 MiB 的限制及网格、原子、身份、
对齐和科学回执检查不变。不降低网格精度，不对电势值舍入，也不虚构配体键级。

配体表面按钮与计算输入共用已验证的化学来源。只有坐标的配体仍可查看三维结构，
但不支持的电性操作会禁用并说明原因；带有可靠配体化学信息的文件继续支持该操作。

Structure previews distinguish viewer initialization, source retrieval, and
structure parsing failures. An explicit, localized Retry reloads the same
displayed source through the existing lifecycle, aborts the previous request,
and disposes its engine. Switching files fences out late responses. There is
no automatic retry loop, source rewriting, or scientific computation rerun;
successful recovery restores reading focus without stealing another control's focus.

结构预览明确区分初始化、文件读取及结构解析失败。原地“重试”只沿现有生命周期
重载当前显示的来源，取消旧请求并释放旧引擎；切换文件后旧响应不能覆盖新结构。
不自动循环重试、不改写来源文件、不重跑科研任务。恢复后保持阅读焦点，
也不抢走用户已转移到其他控件的焦点。

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
