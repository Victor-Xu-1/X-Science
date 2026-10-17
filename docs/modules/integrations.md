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

工作区的“Save as Skill”是个人库变更，不创建对话回合，也不自动运行生成的代码。
[保存入口](../../internal/server/skill_from_conversation_api.go)优先复用当前对话已有的
`SKILL.md` 与同一产物帧中的可选 `kernel.py`；没有这些文件时，由
[作者模块](../../internal/server/conversation_skill_authoring.go)通过当前已保存的模型配置提炼。
草稿优先使用命名的结构化返回；没有工具调用时，也接受整个文本响应恰好为同一 JSON
对象的标准载体。两者共用严格的四字符串字段校验；不从说明文字或代码围栏中
截取/修补 JSON，有其他或多个工具调用时不退回文本，也不执行模型返回的工具。
作者返回的是 Markdown 流程正文；服务端用已校验的 `name` / `description` 字段统一
序列化 `SKILL.md` 元数据，不要求模型再次手写 YAML。若正文仍带完整的前置元数据包裹，
仅当其中包含已声明的名称/描述键时移除该冗余包裹，不把其中的能力或工具声明导入目录；
其他 Markdown（包括分隔线、列表和代码块）保留。空流程仍拒绝安装。现有对话产物
`SKILL.md` 和外部完整 Skill 导入继续走原有校验，不应用此作者专用的元数据生成规则。
保存请求使用通用的 HTTP 运行操作所有权，与聊天/内核共享原子的空闲升级栅栏；
保存期间阻止空闲热更新，成功、失败、取消或 panic 返回后释放。`active_session_runs`
兼容聚合该活动，`active_runtime_operations` 单独展示数量，不伪装成聊天回合；栅栏建立
后拒绝新保存且不调用模型或安装内容。不新增自动重试、权限、持久任务或恢复承诺。
格式无效时只在原超时范围内纠正一次；拒绝、输出截断、权限、网络和
服务错误不作为格式错误重试，不以临时模板代替模型生成的内容。

作者请求沿用所选模型显式配置的输出预算；未配置时沿用供应商默认策略，
不另设竞争性的固定 token 上限。原有总超时、响应字节上限和校验仍然生效。
失败响应提供封闭集合的 `skill_authoring_*` 分类码，页面将其翻译为失败原因；
不显示原始供应商错误、端点、模型草稿或凭据。后端 `conversation_skill_authoring`
诊断仅记录请求关联标识、阶段、分类码、HTTP 状态、耗时及真实输出预算/用量，
以及封闭的返回载体、校验子类和字节/调用计数，不记录原始草稿、工具名或错误内容；
没有观测到的字段不推算，也不把供应商成功状态当作草稿校验或安装成功。

通过校验后沿现有 importer 原子安装到个人目录，并更新同一运行中 catalog；
列表、搜索和加载使用这个目录，不要求重启服务。现有同名保护、可信根目录和执行权限
保持不变。保存成功只表示技能已安装；选择技能通过原有输入框引用，后续发送才进入
正常任务执行链路。`loaded` 与真实工具或 helper 执行仍是不同的状态。

模型和 MCP 服务可能依赖网络、凭据、外部额度或单独安装的运行时。
其 health 或工具列表成功不等于一次科研调用成功。配置操作见
[运维手册](../operations-runbook.md)，执行流程见 [Runtime](runtime.md)。
第三方作者、许可证、校验和与更新说明统一归入
[Third-party inventory](../THIRD_PARTY.md)，不要把“加载来源”写成“原始作者”。

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
