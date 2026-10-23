# Integrations · 扩展接入

Skills 描述如何使用能力，MCP 连接器提供外部工具协议，模型 provider 负责模型服务通信。
三者相互配合但不是同一种插件；显示在目录中不代表已经安装、授权或可执行。

## Find the implementation · 实现入口

| 扩展边界 | 源码与内容入口 | 改动时注意 |
| --- | --- | --- |
| 随产品发布的 Skills | [skills/x-science](../../skills/x-science/) | `SKILL.md`、相关脚本及资料是内容源 |
| Skill 解析、检索和加载 | [skills/loader.go](../../internal/skills/loader.go) · [server/skill_catalog_loader.go](../../internal/server/skill_catalog_loader.go) | 只加载明确可信根目录；个人库与内置库有各自来源 |
| MCP 元数据和用户作用域的执行定义 | [mcpdirectory](../../internal/mcpdirectory/) | 启用状态、凭据授权、目录同步和实际运行身份不能混淆 |
| MCP 协议及进程/远程连接 | [tools/mcpstdio](../../internal/tools/mcpstdio/) | 传输不由目录页面或 Skill 文本实现 |
| 模型服务协议 | [providers](../../internal/providers/) | 请求、流事件、错误和中断处理；不绕过任务运行主链 |
| 网络策略和 TLS 信任 | [networkpolicy](../../internal/networkpolicy/) · [networktls](../../internal/networktls/) | 接入失败不能通过关闭校验或扩大出站权限掩盖 |

## Content and execution · 内容与执行

内置 Skill 的内容摘要清单是
[skills.manifest.json](../../assets/x-science/skills.manifest.json)，生成器为
[scripts/skills-manifest](../../scripts/skills-manifest/)。内容变更后经审阅再重生成，
不要手工维护第二份清单。核心 `x-science-runtime` Skill 由 `BuiltinRuntimeCatalog`
提供，不在 `skills/` 下再复制一份 `SKILL.md`。

Skill 的检索得分只是发现依据，不授予执行权限。MCP 的 runtime resolver
按用户解析连接器、禁用状态及授权；调用时仍须遵守已有工具和权限边界。
连接器目录缓存也不是永远有效的工具 schema，运行路径要使用相应的稳定目录检查。

## Save a conversation as a Skill · 从对话保存技能

工作区的“Save as Skill”不打开保存弹窗。点击沿原有消息入口持久化
`turn_intent: save-as-skill` 的隐藏控制轮次，由
[运行入口](../../internal/server/conversation_skill_control.go)进入当前智能体的正常模型、
工具、租约、检查点、取消与完成投递链路。内部用户气泡隐藏，实际智能体和工具活动仍显示；
提交被接受、对话完成、技能真正发布是三种不同状态，不以假进度或预期文件名代替发布。

[来源投影](../../internal/server/conversation_skill_control_evidence.go)只读取当前分支、
该执行尝试实际接纳输入之前的记录，包括用户需求、修正与真实工具结果，保留有记录的请求
和执行尝试关联。后来排队的输入不混入本次来源；有界历史省略会明确标注，不能推断缺失结果。
当前控制请求要求原智能体先判断是否存在可复用流程；证据不足可以说明原因而不创建技能，
不为保存流程重跑原科研计算。Memory 偏好沿用原对话，但 Memory 不是技能包的存储位置。

技能作者使用已有 Customize / Skill Creator 与控制面 `host.skills`，不再使用独立模型
生成固定四字段或两个文件。通过 `list/read/edit/validate/publish/read` 编辑持久草稿、
验证结构、显式发布并读回真实包，可包含安全的 `scripts/`、`references/`、`assets/`。
结构验证不执行模型生成的 helper，也不把可选评估变成保存每项技能的强制科研门禁。

新草稿位于个人技能根目录的隐藏 `.drafts/` 下；已有 marker 草稿保留兼容作者读取，
不擅自迁移或删除用户数据。草稿不进入普通目录、搜索或执行，编辑已发布技能先形成隔离草稿，
不直接改写 live 文件；实际发布才更新 catalog。发布保留路径、只读来源、同名冲突与显式
覆盖保护，运行器的有效所有者、租约和 kernel generation 在提交边界仍须匹配。

保存过程中输入框尚未发送的草稿和附件保留。重复点击及普通发送共用同一 admission gate；
分支、账户或会话变化后，迟到响应不能修改新状态。重新加载时，只用实际运行尝试对应的
`active_input_message_id` 识别保存任务，不把复用的会话 `turn_id` 当成单次输入身份。
目录重新展开时读取当前个人库；刷新失败只重试读取，不重复保存。

旧 [同步保存 API](../../internal/server/skill_from_conversation_api.go)明确返回 HTTP 410
与升级说明，不再调用模型、猜测产物或安装内容；历史 Skill 建议保留为只读内容。
当前前端只有对话控制这一条保存链路。删除的独立作者、固定返回解析和自动安装实现可由
版本历史恢复，不再构成并行执行权威。

个人库启用与当前专家允许范围仍分开管理。新技能只有实际安装、启用并在当前专家范围内
才可选择；显式“启用并选择”沿现有逐项 attach 流程执行，不替换其他绑定或放开工具权限。
绑定完成后重新读取真实范围，只有仍被允许时才添加输入框引用，不自动发送任务。
加载技能、执行其 helper 与真实科研结果是不同事实，应分别验证。

输入框选择的技能、文件及 MCP 标识保存在规范输入事件的 `inputData` 中。执行准备按
当前有效 runner claim 的输入版本读取最近已接纳的普通消息，而不是读取旧 JSON 会话
或最新排队消息。运行投影不写回会话数据库；新普通消息替换选择，控制回答与恢复保留
同一普通输入。所有者、活动流、租约和当前分支成员关系仍校验，MCP 工具快照也使用
同一 claim 范围；反复绑定模型/工具权限不重复追加同一段输入框上下文。

模型和 MCP 服务可能依赖网络、凭据、外部额度或单独安装的运行时。
其 health 或工具列表成功不等于一次科研调用成功。配置操作见
[运维手册](../operations-runbook.md)，执行流程见 [Runtime](runtime.md)。
第三方作者、许可证、校验和与更新说明统一归入
[Third-party inventory](../THIRD_PARTY.md)，不要把“加载来源”写成“原始作者”。

## Exact public EBI reads · EBI 精确查询

The existing Structures & Interactions connector adds `pdbe_get_validation`
for one experimental PDB entry, alongside its existing RCSB/AlphaFold/EMDB
methods. It preserves experimental method, native nullable quality metrics,
unknown data availability and percentage strings. A recognized absent entry
is distinct from an existing entry whose validation report is unavailable;
HTTP/malformed-data failures are not missing data. These summaries are not a
universal ranking across experimental methods or validation of predictions.

现有 Chemistry 连接器的 `unichem_map_compound` 只接受完整 InChIKey 或精确
ChEMBL ID，保留每个匹配结构以及 PubChem/ChEMBL/ChEBI 的标识映射、真实数量、
本地过滤数量和截断说明，不静默选出一个结构。无目标来源的映射不等于没有结构匹配；
这些是 UniChem 收录的关联，不证明生物学等价或所有来源当前已同步。

Both tools retain the same connector/session/permission, MCP execution and
durable result owners. A declared input schema drives both tool discovery and
direct-call validation using public SDK extension points; conditional fields
and undeclared arguments cannot silently disappear. The SDK still owns actual
execution/output/transport, and the original serving gate still removes
deferred tools. No private SDK handler or separate permission path is installed.

两个查询共用有界 EBI 读取：固定 HTTPS 端点、无跳转、保留宿主代理及 CA 策略，
整个调用25秒（含 PDBe 的多请求）、取消与连接收尾均沿同一生命周期。PDBe 单响应
最多100,000字节，UniChem 最多1,000,000字节；错误不伪装为空结果，也不自动重试。
这不是研究任务的总运行时限。数据归属与使用规则见
[EBI terms](https://www.ebi.ac.uk/about/terms-of-use)；不得把代码许可冒充数据许可。

## Focused verification · 验证入口

从仓库根目录执行：

```sh
go run ./scripts/skills-manifest --check
go test ./internal/skills ./internal/mcpdirectory -count=1
```

这检查发布内容完整性、Skill loader 和连接器目录行为，不会证明所有远端服务在线。
传输改动需追加 `internal/tools/mcpstdio` 的对应协议测试；模型 provider 改动需追加该 provider
的回归与一次实际请求，分别记录外部凭据或网络限制。

[模块导航](README.md) · [Skills 目录说明](../../skills/README.md)
