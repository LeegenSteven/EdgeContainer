# EdgeStack 单文件会议演示

本目录保存会议演示源码。单文件 HTML、ZIP、交互文档和构建记录保留在本地 `deliverables/`，不提交到 GitHub。生成的 HTML 内嵌现部署的 Portainer CE 2.45.3 前端、EdgeStack 中文覆盖层、图标字体和本地模拟响应，直接通过 `file://` 打开。线上 Server 和 Worker 配置不参与演示状态读写。

`demo-runtime.js` 提供设备/资源状态、模拟 HTTP 与 WebSocket、持久化、会议控制和下载。浏览器的 `connect-src 'none'` 限制与模拟传输层共同保证演示离线运行。原生前端组件提供页面、表单、表格、编辑器、权限提示和弹窗；商业功能保留 CE 入口行为。

从固定镜像的 `/public` 目录取得公开资源后，在仓库根目录构建：

```sh
python3 scripts/build-conference-demo.py \
  --public .local/conference/public \
  --output 'deliverables/EdgeStack会议演示/EdgeStack.io-会议演示.html'
```

构建器内嵌脚本和样式，将按需资源转换为 data URL，并仅将 Webpack 动态资源根路径调整为本地内嵌资源入口。原始资源校验和和 HTML 校验和写入 `演示构建记录.json`，开源许可内嵌于 HTML。

使用 Node.js、Playwright 与 Chrome 做离线验证：

```sh
EDGESTACK_TEST_BROWSER=chrome node scripts/test-conference-demo.cjs \
  'deliverables/EdgeStack会议演示/EdgeStack.io-会议演示.html' \
  --flows --audit --controls
```

校验包括页面入口、核心资源变更、Compose 编辑器、分组选择与边缘下发、刷新保留、连接状态切换、导出、重置与登录。测试使用独立浏览器配置，结果保存在 `.local/conference/`。
