# EdgeStack 单文件会议演示

本目录保存会议演示源码。生成的 HTML 及测试产物保存在本地，不提交到源仓库。生成的 HTML 内嵌固定版本的 Portainer CE 2.45.3 公开前端资源、EdgeStack 中文覆盖层、图标字体和本地模拟响应，直接通过 `file://` 打开。线上 Server 和 Worker 配置不参与演示状态读写。

`demo-runtime.js` 提供设备/资源状态、模拟 HTTP 与 WebSocket、持久化、会议控制和下载。浏览器的 `connect-src 'none'` 限制与模拟传输层共同保证演示离线运行。原生前端组件提供页面、表单、表格、编辑器、权限提示和弹窗；商业功能保留 CE 入口行为。

从固定镜像的 `/public` 目录取得公开资源后，在仓库根目录构建：

```sh
python3 scripts/build-conference-demo.py \
  --public /tmp/portainer-public \
  --output '/tmp/edgestack-conference-demo.html'
```

构建器内嵌脚本和样式，将按需资源转换为 data URL，并仅将 Webpack 动态资源根路径调整为本地内嵌资源入口。原始资源校验和和 HTML 校验和写入 `演示构建记录.json`，开源许可内嵌于 HTML。

使用 Node.js、Playwright 与 Chrome 做离线验证：

```sh
EDGESTACK_TEST_BROWSER=chrome node scripts/test-conference-demo.cjs \
  '/tmp/edgestack-conference-demo.html' \
  --flows --audit --controls
```

校验包括页面入口、核心资源变更、Compose 编辑器、分组选择与边缘下发、刷新保留、连接状态切换、导出、重置与登录。测试使用独立浏览器配置；测试结果和浏览器运行记录应保留在本地，不随源代码发布。

新版本默认进入普通用户的「我的空间」，展示 8 台虚构设备及其业务任务。右下角「会议演示 → 管理后台」可切换为原生管理员页面；「用户空间」可切回。所有模拟数据与角色仅保存在这个离线文件的浏览器存储中。

`business-fixture.js` 独立提供 `/api/edgestack` 的演示响应，覆盖设备抽屉、任务、模型与数据位置、语音/视觉文本事件、兼容模型预览与确认、模拟设备回执，以及带事件引用的小结。小结始终标为「离线演示小结 · 本地样例」，不会请求 DeepSeek；示例设备、模型和互动不代表真实环境。系统 Agent 不计入业务任务。

先运行纯合同检查可快速验证 fixture：

```sh
node scripts/test-demo-business.cjs
```

完整浏览器测试始终检查用户空间主要交互，再由 `--flows`、`--audit`、`--controls` 检查原生管理功能与会议控制。
