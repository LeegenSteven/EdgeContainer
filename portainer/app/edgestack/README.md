# EdgeStack 用户工作台组件

用户工作台位于已认证的 Portainer CE 2.45.3 路由中，当前入口为 `#!/workspace?view=space`；旧 `portainer.edgestack` 路由保持兼容。普通用户默认进入工作台，管理员默认进入原生首页，并可显式预览用户界面及返回。

| 源码 | 职责 |
| --- | --- |
| `workspace.js` | 独立导航、SVG设备空间、任务与数据关系、交互时间线、总结和模型预览确认 |
| `workspace.css` | 用户根节点范围内的场景样式、窄屏及减少动态效果 |
| `flow-core.js` | 历史部署配方的纯逻辑：参数、目标、Compose、健康状态和日志帧；保留构建兼容 |
| `deploy/ui/catalog/apps.json`（仓库根目录下） | 历史测试应用的版本化配方，固定镜像和允许参数 |

`scripts/build-ui.py` 从固定上游镜像的原始 `index.html` 生成独立版本资源和清单，原生编译 bundle 保持原样。工作台沿用当前 Angular 注入器、UI Router 和认证守卫，业务请求同源发送到 `/api/edgestack`。浏览器不读取原始登录凭据，不保存云端或摄取密钥。

设备、任务、模型、运行状态、挂载和事件来自授权业务投影。容器 running 不代表正在交互；补传旧事件不播放为实时活动。管理员页面的原生运维能力与用户业务视图分别组织。

`GET /events` 逐授权设备查询，支持时间、类型、会话和游标。用户点击总结或提问后由 Server 组装证据并调用云端。模型更换提交已发布包和预览标识，Server 重新校验版本与权限，真实加载回执确认成功。

```sh
node scripts/test-workspace.cjs
node scripts/test-demo-business.cjs
node scripts/test-scene-ui.cjs
```

以上命令从仓库根目录运行；场景检查需要 Playwright 和可用 Chrome，使用隔离配置及明确模拟数据。中文展示层检查见 `deploy/ui/README.md`，离线完整前端检查见 `demo/conference/README.md`。升级上游时重新验证路由、认证、注入器和 API 兼容性。

完整业务管理页面尚未实现；新增原生 React 页面需要完整编译前端，见根目录《EdgeStack管理员后台补充计划.md》。
