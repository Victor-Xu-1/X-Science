# Workbench · 工作台

工作台负责把研究问题、对话、任务进展、项目文件和科学预览放在同一界面中。
它是 Web 应用；源码目录保留 `packages/desktop` 名称，不代表此目录是另一个后端。

## Find the implementation · 实现入口

| 行为 | 源码入口 |
| --- | --- |
| 登录保护、页面路由和重定向 | [Router.tsx](../../frontend/packages/desktop/src/renderer/components/layout/Router.tsx) |
| 对话页面、消息投影和运行状态 | [pages/conversation](../../frontend/packages/desktop/src/renderer/pages/conversation/) |
| 输入框及 Skill、MCP、文件引用组合 | [components/chat/SendBox](../../frontend/packages/desktop/src/renderer/components/chat/SendBox/) |
| 项目和产物页面 | [pages/project](../../frontend/packages/desktop/src/renderer/pages/project/) · [pages/artifact](../../frontend/packages/desktop/src/renderer/pages/artifact/) |
| 设置与科研工具库 | [pages/settings](../../frontend/packages/desktop/src/renderer/pages/settings/) |
| 类型化跨层调用 | [ipcBridge.ts](../../frontend/packages/desktop/src/common/adapter/ipcBridge.ts) 及其同目录客户端 |

### Navigation · 导航

路由使用 `HashRouter`，浏览器地址包含 `/#/`：

- `/#/login`、`/#/onboarding`：登录及首次配置。
- `/#/guid`：新任务入口。
- `/#/conversation/:id`：具体对话。
- `/#/projects/:projectId`：项目；不是 `/project/:id`。
- `/#/artifacts/:artifactId`：产物预览。
- `/#/settings/:section`：设置；旧入口可由路由重定向到对应页。

## Follow a change · 追踪一次改动

输入框先在前端形成类型化的组合输入，随后经传输适配器交给服务端。
[web_composer_capabilities.go](../../internal/server/web_composer_capabilities.go)
及 [web_composer_runtime_context.go](../../internal/server/web_composer_runtime_context.go)
负责对话授权、能力身份和实际输入材料的服务端处理。
显示某个选项不等于获得执行权限；界面不能自行决定模型、连接器或环境已经可用。

加载、空数据、失败、等待用户和已完成应分别呈现。运行投影也不能替代
[Runtime](runtime.md) 的任务权威或 [Evidence](evidence.md) 的持久化记录。

### Pending decisions and recovery · 待处理事项与恢复

需要用户操作时，输入框上方直接呈现对应入口：真实计划审批显示“查看计划并审批”，
工具授权显示具体操作和批准范围，`ask_user` 显示问题与回答卡片。工具完全访问权限
不等于批准执行计划，也不代替用户回答。多个请求按现有待处理顺序逐项处理，
回答或授权成功后以服务端新快照呈现下一项，不自动替用户决定。

计划只是任务导航数据；模型输出上限或无进展导致的暂停不能因为存在计划而被
标成“等待审批”。暂停显示已保存的进度、可公开的原因和原有继续入口。
状态刷新不关闭用户正在查看的计划；计划版本变更时旧内容和操作失效，必须重新加载。
提交失败在原卡片显示错误并保留回答供重试；过期请求返回冲突时重新读取权威状态，
不让旧问题或旧授权入口一直挡住任务。切换会话后晚到的结果不得作用于新会话。

相关回归及隔离浏览器集成（真实临时 SQLite/HTTP，不启动科研任务或模型）：

```sh
go test ./internal/server -run '^TestFrameAttentionProjectionRequiresAnActualApprovalBoundary$' -count=1
SYNON_FRAME_ATTENTION_BROWSER=1 go test ./internal/server -run '^TestFrameAttentionBrowser$' -count=1
cd frontend
npm run test:unit -- tests/unit/synonbiomed/SynonBiomedRuntimeOperations.dom.test.tsx
```

新任务入口的“发送”沿现有首条消息控制器提交一次请求，保留附件、能力和会话设置，
在实时订阅就绪后发送；不会先降成待用户二次确认的草稿。明确标记为草稿的首次引导
仍只恢复输入，不自动执行。快速重复点击、重新渲染不应重复提交首条任务。
首条请求的既有幂等身份随交接保存，响应不确定后的刷新沿用同一身份，不创建另一轮。

新的 Transcript 输入在 SQLite 事务成功提交后广播通知，空闲执行器收到通知后沿原有
事务领取路径启动；提交失败、回滚、仅暂存和幂等重放不发新通知。通知只用于进程内
唤醒，不授予权限或替代持久化状态。重启及跨进程恢复保留最多 30 秒的空闲轮询后备。
执行器领取、准备和模型首响应是不同阶段；模型响应时间不能当作调度延迟，也不能
用页面动画或降低用户选择的模型思考配置来掩盖。

### Docking preview · 对接预览

自描述对接集合中的共晶参考配体是可选项；没有该参考时仍沿同一对接解析和配体选择
链路打开，默认只显示首个排名姿势，不把设计 SDF 或其他姿势文件自动叠加到场景。
配体列表可逐项切换可见性，只有明确多选或全选才比较叠加；显示不改变真实对接坐标。
普通结构和非对接的版本化母结构/派生结构场景仍保留各自的既有预览行为。

### Durable history recovery · 持久化消息恢复

消息列表是持久化事件的派生视图，不是任务或工具执行的权威。任务取消时，
没有终态回执的前台工具会显示推导出的取消状态；后续执行若显式指向同一
工具的原始执行身份，可以继续该工具，保留同一条工具记录和最终产物引用。
真正的工具终态回执不能被后续“运行中”覆盖，工具身份、输入和父调用也
不能在恢复时改变。恢复后的再次取消按当前执行周期结算，不沿用旧周期。

投影遇到源冲突时保留可用历史并隔离错误；旧快照的构建失败不能隔离已经
推进的源版本。不要直接修改线上投影表或删除原始消息来恢复界面。

工作区 schema 70 将派生表允许的投影版本扩展到 12，并保留已有行。
后台投影流程据版本差异重建旧视图，包括版本 11 下已隔离的会话；不需要
重新执行模型或工具。升级前须备份工作区数据库与运行状态，保留旧部署。
迁移失败时事务回滚；成功升级后采用向前修复，不能把旧程序直接接到已升级
数据库。恢复旧数据库备份会丢失备份后的新写入，必须单独评估和授权。

从仓库根目录运行恢复及隔离竞态回归：

```sh
go test ./internal/server -run '^TestTranscriptToolHistory|^TestTranscriptWebQuarantineRejectsAdvancedSourceFence' -count=1
go test ./internal/persistence/workspace -run '^TestTranscriptWebProjectorV70|^TestTranscriptWebProjectorFresh' -count=1
```

这些测试包含真实临时 SQLite、持久化任务事件和正式投影重建入口；不是
模型调用测试。浏览器场景为 `synonbiomedMessageProjectionRecovery.e2e.ts`，
沿用下文的隔离服务及 Chrome 前置条件。

## Focused verification · 验证入口

### Live context usage · 运行中上下文反馈

输入框圆环只展示主智能体最近一次请求的上下文占用，不是累计计费 Token，也不是
任务完成进度。沿现有 `GET /api/conversations/:id/context-usage` 授权查询读取数值；
响应可选 `progress` 保存流式文本的数值估算和状态，最多每秒持久化一次，不保存文本
或私有推理。结束后由供应商用量校准；旧请求的迟到事件不能覆盖新请求或恢复已删除状态。
工具静默计算不会虚增上下文，未报告的私有推理与工具参数不猜测计数。

运行中或展开面板时每五秒查询。暂时网络故障按 1/2/4 秒有界重试，保留上次读数并
明确标注可能过期；鉴权或无效记录不自动重试，恢复网络、重新聚焦或手动重试可重新查询。
暂无记录显示未知而不是零。历史快照不会被标成当前正在生成。

可选 `autoCompaction` 返回与请求准入共用的有效阈值：默认配置窗口的 80%，已有显式
Token 覆盖或关闭设置原样生效并在界面说明。窗口未配置时显示默认预算，不宣称已经核实
模型容量；供应商实际溢出恢复仍保留。压缩前后的记录不删除用户可见历史。

```sh
go test ./internal/server -run 'TestContextUsage|TestContextProgress|TestContextCompactionPolicy|TestWebContextUsage|TestRequestContextBudget' -count=1
cd frontend
npm run test:unit -- tests/unit/renderer/contextUsage.test.ts tests/unit/renderer/ContextUsagePanel.dom.test.tsx
node tests/web-e2e/contextUsagePanel.browser.mjs
```

### Turn navigation · 左侧轮次导航

桌面会话左侧每次用户提问对应一条导航线；一轮即显示一条，轮次没有固定上限。
悬停或键盘聚焦显示问题及首段答复预览，相邻线条连续伸缩；点击或 Enter 沿现有
消息定位入口跳转，离开、Escape 或正文滚动收起预览。小屏隐藏窄轨，仍可使用顶部
对话检索；启用减少动态效果时停止预览动画与平滑滚动。

导航与顶部检索共用按分支隔离的分页轮次投影，隐藏控制消息、附件续块及工具调用
不会增加轮次。长导航只渲染可见标记并保留首尾与键盘焦点，不截断历史；未加载的
正文通过原锚点窗口加载。远距离定位由虚拟列表的实测跳转完成，近距离才平滑移动，
避免变高消息的估算位置导致跳错。导航失败提供重试，不改写消息或触发模型执行。
重叠分页按消息身份去重，不按文字去重。显式锚点跳转暂停历史自动翻页，直到用户
再次主动滚动；分页还需检查实际视口边界，不能把虚拟列表预渲染边界当成滚动位置。
现有滚动控制器保留显式定位意图，在报告、图片等延迟内容改变高度时通过虚拟列表
重新对齐目标；用户主动滚动后立即释放，不使用定时重试或直接改写正文 scrollTop。
虚拟行通过独立格式化上下文包含子消息外边距，确保行间距参与高度测量，避免
长列表跳转的累计偏移以及目标与轮次高亮不一致。
进入会话后新增的已接受轮次保留在当前分支索引中，切换正文窗口不会删除标记。
分支索引与正文窗口的分支身份一致时才合并实时消息，避免切换期间混入旧分支。
长导航支持独立鼠标滚轮浏览和顺序键盘访问，预览仅保留有界文本片段。

```sh
cd frontend
npm run test:unit -- tests/unit/renderer/conversationTurnIndex.test.ts tests/unit/renderer/conversationTurnIndexLoading.test.ts tests/unit/renderer/ConversationTurnRail.dom.test.tsx
node tests/web-e2e/turnRail.browser.mjs
```

### Completed round details · 每轮完成信息

已完成的最终答复下方保留复制、从此回复分支到新会话、完成时间、实际运行耗时和无下划线的“调用”入口。浮层按该轮的
持久化模型调用记录展示输入、缓存读取、缓存写入、输出、总计及实际模型；
输入总量包含未缓存输入、缓存读取与缓存写入，后者在输入项下缩进显示，不与输入总量
重复累加。用量条仅按输入总量和输出划分，表示 Token 构成而非任务进度；传输字段
`tokens.input` 仍是跨供应商规范化后的未缓存输入。供应商报告总计无法与输入、输出
核对时保留原始数字并说明差异，不强行归一化成看似正确的占比。

底栏的图标、点击区域和文字基线采用统一样式；窄屏自然换行。悬停、键盘焦点、
禁用与调用浮层展开状态明确区分，分支处理中显示加载图标，不改变原有请求与统计行为。
不会使用当前选择的模型或整个会话的累计用量替代历史轮次。不同供应商的缓存口径
被换算为互不重叠的展示分类，总计仍以供应商回执为准。

同一输入的恢复尝试计入该轮，空闲等待不计入实际运行耗时；后续输入、迟到的审计记录
和已放弃分支不改变已完成轮次。没有可核验用量时显示“未记录”，部分回执缺失时
明确标记统计不完整。历史消息和实时完成事件使用同一后端统计投影；浮层显示不改写
答复正文、不重复执行模型，也不影响每轮结束后的生成物挂载。

分支按钮使用既有 `POST /api/conversations/clone` 事务链路，追加可选的
`through_attempt`（已完成回复对应的运行序号）和 `source_branch_id`。
不传截止点时仍复制完整会话。指定截止点时只复制所选分支截至该完成回执的
消息、工具历史和产物版本引用，并建立独立的新会话；不复制后续轮次或其他分支，
不自动执行新任务。原会话与原文件不被修改。运行中禁用按钮，服务端仍独立验证
归属、已完成边界与一致性。一次点击的 `intent_id` 在失败后的显式重试中保持不变，
避免响应丢失导致重复创建。没有完成回执的中间文本不提供这个入口。

分支保留已继承回复的调用统计和文件卡片：统计沿不可变复制收据读取原始调用，
新轮次只统计新会话的调用；文件只解析复制边界内的具体版本，原会话后续更新不会
替换旧版本。工作区中的继承文件为只读，不转移原文件归属；同样支持再次分支。

```sh
go test ./internal/server -run '^TestRoundUsage' -count=1
cd frontend
npm run test:unit -- tests/unit/common/roundSummary.test.ts tests/unit/renderer/MessageRoundFooter.dom.test.tsx
```

分支的受控浏览器集成使用临时 SQLite 与真实本地 HTTP，不连接已有用户会话：

```sh
SYNON_REPLY_BRANCH_BROWSER=1 go test ./internal/server -run '^TestWebReplyBranchBrowser$' -count=1
```

### Document preview · 文档预览

回答中的生成文件链接以蓝色粗体显示，点击或键盘激活进入应用内预览；
下载是预览内的独立操作。产物引用、文件名和产物内容 URL 共用会话产物
解析器；`#/artifacts/` 工作台链接也沿此入口打开，不另开网页。内容 URL 与工作台
链接只提供待核验的产物/版本标识，不会请求模型写入的外部
域名；找不到或版本不匹配时显示错误，不自动跳转网页或下载。

PDF 的对话、文件画板和产物详情共用 PDF.js 渲染，不依赖浏览器内置 PDF
插件。已发布产物使用带鉴权的内容 URL；工作区本地 PDF 经现有
`/api/fs/read-buffer` 权限与大小校验读取，不会把服务端路径转换为
浏览器 `file://` URL。预览加载失败时显示错误并可重试。
PDF.js 的中文字符映射、标准字体和图像解码资源随前端按依赖版本打包，
无需第三方 CDN。内容下载限制为 64 MiB（本地文件读取仍受 32 MiB API
限制），页画布按滚动视窗挂载；超限文件保留下载入口，不无限占用浏览器内存。

DOCX 和 PPTX 当前提供正文/幻灯片文字预览，不是原始分页、图表或排版的
完整还原。XLSX 支持工作表切换、空白行和已保存的公式缓存值；预览不会
执行公式。轻量转换入口是 `/api/document/convert`，其文件路径授权及
产物/版本归属检查均由服务端执行。
轻量转换不支持旧版二进制 DOC/PPT、宏文档及归档内的 Office 子文件；
这些场景需下载后用对应应用打开，不应将它们与现代格式主入口验收混为一谈。

已在 `frontend/` 安装锁定依赖后，从该目录执行：

```sh
npm run test:unit -- tests/unit/renderer/routeModules.test.ts tests/unit/renderer/conversation/composerCompositionModel.test.ts tests/unit/renderer/conversationRuntimeViewStore.test.ts
```

这组测试检查路由模块、输入组合和运行视图状态，不启动真实后端。
交互或传输改动还需选取 [tests/web-e2e](../../frontend/tests/web-e2e/) 中对应场景，
在隔离后端、明确测试账号及 Google Chrome 下验证；配置和前置条件见
[frontend README](../../frontend/README.md)。不要把默认测试 URL 当作当前部署地址。

真实界面图片必须来自实际运行的浏览器，不能由生成图片替代。
概念视觉只能说明设计意图，不能证明页面、任务或科学结果已实现。

[模块导航](README.md) · [工程拓扑](../engineering/module-topology.md)
