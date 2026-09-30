# Runtime · 任务运行

Runtime 将一次用户请求组织为模型交互、工具调用、验证和结果提交。
逻辑任务可以跨执行周期继续；单次模型、工具或进程操作仍有自身边界。
暂停、取消、进程退出和任务完成不是同一个状态。

## Find the implementation · 实现入口

| 责任 | 源码入口 | 边界 |
| --- | --- | --- |
| 阶段顺序与合法转换 | [sessionrunner/machine.go](../../internal/sessionrunner/machine.go) | 校验阶段转换，不独自实现调度或存储 |
| 一次执行周期的组合 | [server/runner_cycle.go](../../internal/server/runner_cycle.go) | 连接 claim、恢复、上下文、模型、工具及提交适配器 |
| 模型执行引擎 | [agentruntime](../../internal/agentruntime/) | 模型循环、工具批次和结果物化；服务商传输由 [providers](../../internal/providers/) 承担 |
| 固定顺序的工具流水线 | [toolgateway/pipeline.go](../../internal/toolgateway/pipeline.go) | 统一执行前检查、权限、执行后处理和审计 |
| 原生工具定义与注册 | [tools/registry](../../internal/tools/registry/) | 区分模型可见工具与服务操作；动态 MCP 仍由 MCP transport 执行 |
| 持久化对话与运行记录 | [persistence/transcript](../../internal/persistence/transcript/) · [persistence/workspace](../../internal/persistence/workspace/) | server 消费存储接口，不以 UI 状态代替 durable authority |

## State and recovery · 状态与恢复

正常周期的阅读顺序为：

```text
claim → recovery → context → snapshot → provider ⇄ tool → verify → complete
```

这只是正常路径示意，不是完整枚举。验证可以要求继续模型修正；`paused`、`terminal`
及其他合法转换以 [machine.go](../../internal/sessionrunner/machine.go) 为准。
不要把阶段名直接当成用户可见的任务终态。

工具处理的顺序由 `NewPipeline` 检查。权限拒绝等提前结束路径仍需进入审计，
不能用新的 HTTP 路由或恢复回调绕开原有执行入口。详细不变量见
[Harness conformance](../engineering/synon-harness-conformance.md) 和
[架构机器契约](../governance/harness-architecture.json)。

恢复会检查任务归属、执行身份和已有回执；可持久化的文件与检查点不等同于解释器内存快照。
进程已退出时不能仅凭最近心跳声称仍在运行。科学进程的创建、取消与资源约束见
[Science](science.md)，产物及事件提交见 [Evidence](evidence.md)。

自动生成的工作计划是导航状态，不是第二套完成判据。最终答复通过任务契约、实际执行
回执和产物完整性检查后，未更新完的计划状态只记录为提示，不自动重开一轮模型调用、
重复计算或重新发布；原计划的待办状态保留，不能为了放行而伪造完成。
用户明确审阅并批准的执行计划仍保留原有执行义务，鉴权、归属和真实失败检查不变。

恢复阶段完成的内核调用和正常工具阶段沿同一持久化回执路径识别。阶段名用于观察，
不是执行是否发生的依据；非终态、未执行的前置拒绝和失败回执不能冒充成功。
计划状态反馈保留 `execution_ref`、`execution_binding` 和 `execution_continuation`，
模型可以复用匹配的已有执行，不得因为反馈压缩丢失标识而再次执行。
重新执行请求按相邻动作识别否定语义：明确“不重新计算”不产生新计算义务；
同一消息中另有明确重新分析的要求时，该要求仍须有当前请求的成功执行证据。

对应回归（含真实 SQLite、终态协议结算和可控 HTTP 模型边界）：

```sh
go test ./internal/server -run 'TestCompactPlanExecutionRepair|TestCompletionPlanPolicy|TestRecoveredToolContract|TestAutonomousCompletionPublishesFinal|TestForegroundDetachedKernelWaitDoesNotCommitProvisionalTerminalReceipt|TestExecutionPlanStepRequiresScoped|TestExecutionReceiptAmbiguityAndPlanFence' -count=1
```

## Focused verification · 验证入口

### Background shutdown · 后台执行关闭

后台 Shell 的生命周期包括启动、进程退出、输出日志和任务终态持久化。
关闭服务时先封闭新启动的入口；已经获准、但尚未登记进程的启动也属于等待范围，
登记时发现关闭已开始就立即停止进程。只有全部收尾完成后，关闭才报告成功。
等待遵守调用方的取消和期限；日志或任务状态写入失败会明确记录并反馈，不以进程
消失或清空进程表代替成功收尾。
任务存储保留执行器的 `failed` 终态及退出码；普通命令失败不是关闭失败。
任务文件读写不占用启动/关闭互斥锁，文件系统阻塞时关闭仍能遵守调用方期限。

```sh
go test ./internal/server -run 'TestServerClose.*BackgroundShell|TestBackgroundShell' -count=1
```

### Provider generation recovery · 模型生成恢复

环境默认模型和已保存模型使用同一任务隔离的输出预算恢复逻辑。供应商返回输出截断后，
根据实际用量调整下一次调用额度；用户明确设置的额度、调用级额度和供应商确认的上限
仍具有权威。配置和凭据不会因恢复而被改写。

显式额度不跳过恢复判定。生成失败的进展与重试条件使用同一个持久化检查点，
包括没有正文、工具参数尚未完整及模型返回空响应的路径。计数绑定当前输入和
提供方/模型/额度身份；真实新证据、有效输出或改变配置可以改变恢复决策，
准备日志和相同执行观察不能。输出截断本身不是成功的分段，不会仅凭错误码立即重排。
下一次生成的退避截止时间与观察一起持久化；普通运行池和恢复调度器均在请求前
遵守该截止时间，等待可取消，不会因重启或重新领取租约而跳过。

续写回执始终保留已接受的精确文字。连续截断只增加少量字节时，这些字节不再单独证明
任务取得有效进展；原恢复链改为完成下一步动作，必要时把过大的单个动作拆开执行。
若供应商无法提供更大的有效额度，且改变生成方式后仍只产生碎片，保存原任务与检查点，
等待条件改变或用户继续；不把它标为任务完成或丢弃原消息。持续增长的输出、正在进行的
工具及长计算不受这条同路径恢复规则限制。

```sh
go test ./internal/server -run 'TestStaticOutputBudget|TestOutputBudget|TestContinuation.*Generations|TestContinuationGrowingBudget' -count=1
```

### Reading progress · 读取进展

任务内重复读取相同来源和窗口时，以实际返回内容判断是否有新证据，不以模型附带的
说明文字或新的工具调用编号判断进展。普通文件每次仍通过原权限检查并读取当前内容；
相同窗口内容未变才返回复用标记，文件变化和后续窗口继续正常执行。
观察摘要随已完成回执恢复，且使用任务内有界缓存，不跨项目或任务共享。
同一末页的等价窗口不会因为请求了更多行而被当成新证据。
恢复从活动分支的权威事件流重建观察，不复用为模型裁剪的最近消息窗口；
准备日志不能把已读取的证据变成首次读取。解释器的重复可观察结果仅用于恢复
进展判断，不缓存执行，也不声称未知外部副作用已经被证明不存在。

缓存来源与文件读取的混合批次沿原无进展恢复链处理：模型先获得复用已有证据、改变
下一步动作的反馈；持续重复则保存恢复点，不重复网络副作用，也不靠固定任务时长终止
健康下载或计算。大结果仍遵守原有不可变回执与读取权限，不能将预览当作全文。

```sh
go test -race ./internal/server -run 'TestReadProgress|TestSessionRunnerReadReuse|TestServerAgentRuntimeReadReuse|TestCorrectionRead|TestCorrectionPaged|TestNoProgressReceipt' -count=1
```

从仓库根目录执行：

```sh
go test ./internal/sessionrunner ./internal/toolgateway -count=1
```

这验证阶段转换和工具顺序，包括架构契约一致性；它不覆盖所有 server 恢复适配器，
也不是一次真实模型任务。修改恢复、取消或完成逻辑时，继续选择
`internal/server` 下直接相关的回归，再用实际模型或工具协议复测同一行为。
凭据、网络或运行环境不可用时，应分别记录，不能用模拟响应宣称真实链路通过。

[模块导航](README.md) · [工具流式呈现契约](../engineering/tool-stream-presentation-contract.md)
