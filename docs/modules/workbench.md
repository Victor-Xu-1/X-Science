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

## Focused verification · 验证入口

### Document preview · 文档预览

回答中的生成文件链接以蓝色粗体显示，点击或键盘激活进入应用内预览；
下载是预览内的独立操作。产物引用、文件名和产物内容 URL 共用会话产物
解析器。内容 URL 只提供待核验的产物/版本标识，不会请求模型写入的外部
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
