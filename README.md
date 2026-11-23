# EdgeStack

观看手机、管理员和普通用户的带章节操作视频，或下载离线交互包，从各自的登录页体验完整流程。[手机 MP4](https://github.com/LeegenSteven/EdgeContainer/releases/download/demo-2026-11-23/edgestack-mobile.mp4) · [管理员 MP4](https://github.com/LeegenSteven/EdgeContainer/releases/download/demo-2026-11-23/edgestack-admin.mp4) · [用户 MP4](https://github.com/LeegenSteven/EdgeContainer/releases/download/demo-2026-11-23/edgestack-user.mp4) · [交互包与发布说明](https://github.com/LeegenSteven/EdgeContainer/releases/tag/demo-2026-11-23)

基于 Portainer CE 的端侧设备管理与用户可视化工作台，保留原有 `portainer/`、`agent/` 源码及许可证。

普通用户通过浅色 2.5D 设备空间查看授权设备、业务任务、模型、数据位置和交互记录，按需生成带事件引用的总结，并更换管理员已发布的兼容模型。管理员默认进入原生完整管理界面，可显式预览用户视图。

## 源码与文档

| 资料 | 用途 |
| --- | --- |
| [用户工作台开发文档](EdgeStack用户可视化工作台开发文档.md) | 产品设计、事件协议、接口与实现入口 |
| [用户工作台实施记录](docs/用户可视化工作台实施记录.md) | 已验收范围、实现差异与后续工作 |
| [管理员后台补充计划](EdgeStack管理员后台补充计划.md) | 待实施的业务管理页面、网页密钥配置及备份扩展 |
| [业务 API](portainer/api/edgestack/README.md) | 任务登记、来源凭据、权限和模型契约 |
| [业务 Server 构建与回退](deploy/server/README.business.md) | 固定基线、自定义后端、配置和回退 |
| [Worker 业务事件适配器](worker/business-events/README.md) | JSONL 事件、持久队列、语音钩子和测试入口 |
| [中文界面与品牌层](deploy/ui/README.md) | 静态资源构建、安装、版本清单与回退 |
| [离线演示源码](demo/conference/README.md) | 明确模拟的数据、单文件构建和交互检查 |

## 当前实现

- 用户界面位于 `portainer/app/edgestack/`，以独立 JS/SVG/CSS 模块加载；原生管理员编译资源沿用固定基线。
- Server 业务模块编入 Go 后端，独立业务库位于 `/data/edgestack/business.db`。业务 ACL 与原设备权限共同约束查询及操作。
- Worker 从业务程序专用 JSONL 读取最终交互，使用独立 SQLite 队列重试；摄取凭据、语音上游凭据和云端凭据分别管理。
- 总结由用户点击触发，Server 选择授权证据、脱敏、调用 DeepSeek 并校验引用；普通操作不自动触发云端调用。
- 模型更换只接受已发布包和预览标识，通过设备真实加载回执确认结果。实例运行、模型加载和业务互动分别显示。

真实设备验收覆盖一台 RK3588 的 ASR/TTS、真实麦克风测试入口、事件补传去重、云端总结，以及同一模型家族编码窗口包的更换与恢复。语音测试为回声回复；完整对话模型、真实摄像程序、更多模型家族和多板硬件另行接入验收。

## 构建与检查

Server 使用 `portainer/go.mod` 声明的 Go 工具链：

```sh
scripts/build-business-server.sh /tmp/edgestack-business-context
```

按照 Server 和 UI 文档先构建后端镜像，再从固定镜像导出的原始 `/public/index.html` 构建界面资源，清单绑定不可变后端镜像。新增原生管理员页面的完整前端编译属于后续后台计划。

核心检查命令：

```sh
(cd portainer && go test -race ./api/edgestack ./api/http/handler/edgestackbusiness ./api/http/handler/edgestacks ./api/http)
python3 -m unittest discover -s worker/business-events/tests
node scripts/test-workspace.cjs
node scripts/test-demo-business.cjs
```

浏览器检查脚本 `scripts/test-ui.cjs`、`scripts/test-scene-ui.cjs` 和 `scripts/test-conference-demo.cjs` 使用隔离的 Playwright/Chrome；具体输入见各文档。生成的 HTML、ZIP、截图、浏览器记录、数据库和构建产物不提交。

真实设备配置、密钥与业务记录由部署者在私有目录配置。仓库示例不包含生产凭据；`deploy/images.lock.json` 保存固定上游版本与公开镜像 digest。
