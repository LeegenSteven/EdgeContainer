# EdgeStack 单文件会议演示

本目录保存会议演示源码。手机、管理员和普通用户的操作视频与交互包通过 GitHub Releases 分发，本地副本位于 `deliverables/`。单文件 HTML 内嵌 Portainer CE 2.45.3 前端、EdgeStack 中文界面和模拟响应，可直接通过浏览器打开。[下载 2026-11-23 演示](https://github.com/LeegenSteven/EdgeContainer/releases/tag/demo-2026-11-23)

发布包使用 `--login` 构建。管理员入口预填 `admin`，用户和手机入口预填 `viewer`，演示密码均为 `demo`。提交原生登录表单后进入相应工作区。两份角色文件分别保存模拟数据。要在同一组模拟数据中查看申请与管理员回复，可以通过右下角的「会议演示」切换到另一角色的登录页。

手机入口 `mobile/index.html` 在手机浏览器打开用户端，在宽屏显示 390 像素的交互预览。打包时将 `release-index.html` 作为根目录的 `index.html`，将 `mobile-preview.html` 复制到 `mobile/index.html`，并将用户端与管理员端分别输出为 `user/edgestack-user.html` 和 `admin/edgestack-admin.html`。下面两个构建命令省略 `--login` 时，适用于跳过登录的会议现场演示。

`demo-runtime.js` 提供设备/资源状态、模拟 HTTP 与 WebSocket、持久化、会议控制和下载。`business-fixture.js` 与 `admin-fixture.js` 共用业务示例，分别支持用户工作台和七个管理员业务页面。浏览器的 `connect-src 'none'` 限制与模拟传输层共同保证演示离线运行。原生前端组件提供页面、表单、表格、编辑器、权限提示和弹窗，商业功能保留 CE 入口行为。

输入目录须包含同次编译的原生前端、匹配的中文展示层和用户工作台资源。原生前端通过 `scripts/build-native-ui.sh` 编译，展示层通过 `scripts/build-ui.py` 生成，合并公开资产后在仓库根目录构建两个入口：

```sh
python3 scripts/build-conference-demo.py \
  --public '<配套前端公开资源目录>' \
  --persona admin \
  --output 'deliverables/EdgeStack会议演示/EdgeStack.io-会议演示.html'

python3 scripts/build-conference-demo.py \
  --public '<同一配套前端公开资源目录>' \
  --persona user \
  --output 'deliverables/visual-workspace/EdgeStack用户空间离线演示.html'
```

构建器内嵌脚本和样式，将按需资源转换为 data URL，并仅将 Webpack 动态资源根路径调整为本地内嵌资源入口。原生主包、界面版本、模拟器源码、默认角色、资源及 HTML 校验和写入 `演示构建记录.json`，开源许可内嵌于 HTML。可用 `--frontend-commit` 登记原生前端来源提交。

使用 Node.js、Playwright 与 Chrome 做离线验证：

```sh
EDGESTACK_TEST_BROWSER=chrome node scripts/test-conference-demo.cjs \
  'deliverables/visual-workspace/EdgeStack用户空间离线演示.html' \
  --flows --audit --controls --output deliverables/验证记录/conference

EDGESTACK_TEST_BROWSER=chrome node scripts/test-offline-admin.cjs \
  'deliverables/EdgeStack会议演示/EdgeStack.io-会议演示.html' \
  deliverables/验证记录/offline-admin
```

校验包括页面入口、核心资源变更、Compose 编辑器、分组选择与边缘下发、刷新保留、连接状态切换、导出、重置与登录。测试使用独立浏览器配置，截图与 JSON 报告默认保存在 `deliverables/验证记录/conference/`。管理员交付件检查默认保存在 `deliverables/验证记录/offline-admin/`。

当前离线交付件的界面版本为 `82e2fa3b8578`。用户版默认进入普通用户的「我的空间」，展示 8 台虚构设备及其业务任务。点击左上角「菜单」打开导航抽屉，选择「我的空间」「我的任务」「互动记录」或「模型与数据」后收起。关闭按钮、遮罩和 Esc 都可收起抽屉。首页主区展示设备空间，互动记录从菜单或设备详情进入。功能图标采用本地内嵌的 Tabler Icons v3.52.0，许可随源码和 HTML 保留。管理员版默认进入原生管理首页。

此处记录离线交付版本。生产环境最后记录的已核验 UI 为 `ae6a414f3e96`（2026-11-15）。实际生产版本以 Server 的部署及回读核验记录为准。右下角「会议演示 → 管理后台 / 用户空间」可切换角色。两份文件分别使用 `edgestack.conference.v3.user` 与 `edgestack.conference.v3.admin` 保存状态；刷新保留修改，重置恢复当前文件的初始角色与示例。

管理员页面支持登记资料编辑与版本冲突、授权核对、来源凭据一次显示、模型草稿验证及发布撤回、恢复预览、操作审计和云端设置的本地模拟。云端设置可输入演示文本检查表单流程；候选 Key 用后清空，不进入存储或导出，也不请求真实云端。

`business-fixture.js` 独立提供 `/api/edgestack` 的演示响应，覆盖设备抽屉、任务、模型与数据位置、语音/视觉文本事件、兼容模型预览与确认、模拟设备回执，以及带事件引用的小结。小结始终标为「离线演示小结 · 本地样例」，不会请求 DeepSeek；示例设备、模型和互动不代表真实环境。系统 Agent 不计入业务任务。

设备、任务、来源、模型和数据位置按当前角色与授权范围展示。设备管理连接、任务运行观察和模型加载回执分别标明。互动时间线可按设备、最近 24 小时或 7 天、类型和会话筛选，部分设备读取失败时保留已加载记录并提供定向重试。小结单独选择最近 1 小时、6 小时、24 小时或 7 天，整理该设备的授权对话及观察文字，播放状态排除在小结输入之外。「查看依据」定位到匹配原文。离线版的以上数据与结果均为本地模拟。

先运行纯合同检查可快速验证 fixture：

```sh
node scripts/test-demo-business.cjs
```

完整浏览器测试始终检查用户空间主要交互，再由 `--flows`、`--audit`、`--controls` 检查原生管理功能与会议控制。
