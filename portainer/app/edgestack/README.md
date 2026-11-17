# EdgeStack 用户工作台

用户工作台在 Portainer CE 2.45.3 的已认证 `root` 路由下注册 `portainer.workspace`，入口为 `#!/workspace?view=space`，兼容 `portainer.edgestack` 旧入口。页面挂载于 `content@`，沿用 Angular UI Router 和原有认证守卫。

## 页面与导航

普通用户登录后进入「我的空间」。点击左上角「菜单」打开导航抽屉，进入「我的空间」「我的任务」「互动记录」或「模型与数据」。选择页面、点击关闭按钮或遮罩、按 Esc 均可关闭菜单。管理员默认进入原生管理首页，通过「用户视图预览」进入工作台，通过菜单返回管理后台。

- 我的空间展示授权设备和管理连接状态，支持名称搜索、在线筛选及空间/列表视图。窄屏默认列表，点击设备打开详情。
- 我的任务展示已登记的业务任务、独立的运行观察和当前模型。设备详情包含概览、互动记录、模型与数据三个页签。
- 互动记录支持设备、时间、类型和会话筛选。全部设备视图逐条标明设备名，部分读取失败时保留已加载记录并提供失败设备重试。
- 模型与数据展示任务模型、模型状态、登记目录及实际挂载核验结果，也可查看设备、任务与数据的关系。更换模型经过兼容候选、变更预览、确认提交和设备回执，当前标签页刷新后可恢复已保存操作的进度。

功能图标采用本地内嵌的 Tabler Icons v3.52.0，MIT 许可和来源随 `workspace.js` 保留。首页主区集中展示设备，完整互动时间线从菜单或设备详情进入。

## 数据范围与认证

浏览器使用既有同源登录会话，通过 `credentials: 'same-origin'` 请求 `/api/edgestack`。Server 核对设备 ACL、任务 ACL 与原生环境权限，页面展示当前账号的授权结果。设备管理连接、任务运行观察、模型加载回执和数据位置核验分别展示。

互动时间线可选最近 24 小时或 7 天，并叠加事件类型、会话筛选。设备小结单独选择最近 1 小时、6 小时、24 小时或 7 天。生产环境仅在点击生成后把该设备所选时段的授权对话、观察文字及填写的问题发送到 DeepSeek，播放状态排除在小结输入之外。结果标明实际时段，话题引用可定位原文。离线 HTML 使用虚构本地记录和本地小结。

页面在内存保存当前账号的筛选、布局和滚动位置。模型操作标识按账号及角色存入当前标签页的 `sessionStorage`，读取操作结果仍经 Server 鉴权。组件销毁时取消请求、计时器和事件监听。

## 源码与接口

- `workspace.js` 负责业务视图、菜单抽屉、认证路由集成、API 调用和组件生命周期。
- `workspace.css` 负责工作台、导航和设备详情的布局、状态及响应式样式。
- `flow-core.js` 与 `deploy/ui/catalog/apps.json` 保留部署配方、目标校验和历史流程回归所用逻辑。

用户端主要调用以下业务接口。

| 行为 | 接口 |
| --- | --- |
| 设备及来源 | `GET /api/edgestack/devices`、`GET /devices/{id}`、`GET /devices/{id}/event-sources` |
| 互动记录 | `GET /api/edgestack/events`，带设备、时间、类型、会话和游标参数 |
| 兼容模型与预览 | `GET /api/edgestack/tasks/{id}/models`、`POST /tasks/{id}/model-change-preview` |
| 模型提交及结果 | `POST /api/edgestack/tasks/{id}/model-changes`、`GET /operations/{id}` |
| 互动小结及结果 | `POST /api/edgestack/summaries`、`GET /summaries/{id}` |

表中缩写接口均以 `/api/edgestack` 为前缀。语音接入测试由具备对应能力的任务提供同源入口和授权代理。

## 构建、验证与版本

在仓库根目录用 `scripts/build-ui.py` 为匹配的原生前端生成版本化工作台资源，再用 `scripts/build-conference-demo.py` 构建用户与管理员离线 HTML。各 HTML 旁的 `演示构建记录.json` 保存实际版本、来源和 SHA256。

当前离线交付件 UI 为 `82e2fa3b8578`。生产环境最后记录的已核验 UI 为 `ae6a414f3e96`（2026-11-15），生产版本以实际部署与回读记录为准。

验证结果默认写入 Finder 可见的本地目录。

| 检查 | 默认报告目录 |
| --- | --- |
| `scripts/test-conference-demo.cjs` | `deliverables/验证记录/conference/` |
| `scripts/test-scene-ui.cjs` | `deliverables/验证记录/workspace/` |
| `scripts/test-offline-admin.cjs` | `deliverables/验证记录/offline-admin/` |
| `scripts/test-admin-native.cjs` | `deliverables/验证记录/admin-native/` |

这些目录保存截图和 JSON 报告，交付 HTML、ZIP 与构建记录保存在本地 `deliverables/`。发布前运行纯逻辑检查与隔离浏览器回归，覆盖用户导航、设备详情、筛选、模型操作、小结引用和管理员入口。升级 Portainer 时重新验证路由、认证、API 与注入器兼容性。
