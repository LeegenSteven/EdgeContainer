# EdgeStack.io 简体中文界面

适用版本为镜像锁定清单中的 Portainer CE **2.45.3**。本目录构建中文品牌展示层及 EdgeStack 五步操作组件。后端、原生前端包、公共 API 和设备身份沿用现有版本；新增组件由独立静态资源加载。

当前覆盖 Docker、全局管理和边缘计算的固定界面文案；Kubernetes/Azure 专有页面需要在启用对应环境时补充。代码、命令、日志、表单实际值与资源名称不通过词表翻译。帮助链接仍指向对应的上游文档。版权和许可文件保留，页脚注明基于 Portainer 社区版。

## 日常操作与完整管理

普通用户工作台提供设备、业务任务、文本事件与小结入口。应用部署向导与完整管理界面继续提供原有操作能力。部署向导流程为：

**选设备 → 选应用和版本 → 确认必要参数 → 部署 → 看状态和日志。**

当前提供两个已固定 ARM64 镜像摘要的配方：连通性测试、持久化测试网页。业务应用可继续通过原有完整界面管理；新增配方在 `catalog/apps.json` 中维护，并通过构建和设备验证后发布。

首次使用一台设备时，在参数页填写“设备业务地址”，例如该板子的局域网 IP。它只用于生成访问链接，保存在当前浏览器的非敏感偏好中；管理员已配置的环境 PublicURL 也可提供初始值。Edge 环境的连接 URL 指向 Server，因此业务链接不会从该字段推断。

基础页按应用显示实例名称、访问地址、端口及数据参数；完整 Compose 可在“高级设置”查看或编辑。自定义内容保留在当前向导的内存中，返回前一步不会清空。历史应用的完整配置、启停、更新、终端和 Inspect 继续使用原生页面。

“高级管理”显示原侧栏和全部现有功能；管理员入口提供接入、分组、仓库、用户和系统设置。界面分层沿用后端权限，CE 的边缘应用栈下发仍要求管理员。提交前确认静态分组恰好只包含所选的一台设备，并检查在线情况、架构、同名部署、已知端口和数据卷。

应用详情分别显示部署结果、容器运行、业务健康。没有健康检查时明确标为“未配置健康检查”；设备失联时显示待确认。部署结果 URL 包含后端部署编号，刷新会重新读取结果。日志与资源占用均从当前设备读取。

## 构建与验证

从正在运行、使用固定镜像的 Server 中取得**公开静态文件**，不需要管理员密码或 Edge 凭据：

```sh
docker cp edgestack-server:/public/index.html /tmp/upstream-index.html
```

已有覆盖包时使用首次安装保留的 `$HOME/edgestack/ui-backups/upstream-index.html`。然后在仓库根目录构建到新的空目录：

```sh
python3 scripts/build-ui.py --index /tmp/upstream-index.html --output /tmp/edgestack-ui-release
node scripts/test-ui.cjs /tmp/edgestack-ui-release
node scripts/test-workspace.cjs
```

测试需要 Node.js、Playwright 及其 Chromium；使用系统 Chrome 时设置 `EDGESTACK_TEST_BROWSER=chrome`。测试使用临时独立配置，不读取已有浏览器会话。

产物包括 `index.html`、`edgestack/` 下带内容版本的 JS/CSS、SVG 标识和 `manifest.json`。清单记录所有产物校验和、原始入口页校验和及适用镜像。

五步组件源码位于 `portainer/app/edgestack/`，构建器将配方目录嵌入独立组件并生成版本资源。中文词表层继续只处理文案。完整离线前端构建后，可运行 `node scripts/test-workspace.cjs '<离线 HTML 路径>'` 验证向导、日志、端口冲突和高级配置保留。

## 安装与回退

将产物目录及 `scripts/install-ui.sh` 传到 Server 的 WSL 私有项目目录，在 WSL 执行：

```sh
bash install-ui.sh /tmp/edgestack-ui-release edgestack-server --persistent
```

安装器核对镜像和文件校验和，保留上一个入口页，将公开资源复制到 `~/edgestack/server/ui/`，并将 `deploy/server/compose.ui.yaml` 安装为配置目录内的 `compose.override.yaml`。仅对原 Server 服务运行 Compose，原命名数据卷保持挂载。首次启用需要重建 Server，网页可能要求原 admin 重新登录；后续同样挂载结构下更新文案不重启进程。

入口页和资源目录以只读 bind mount 加载，容器重启、重建和 Windows 重启后都保留。升级 Portainer 前先针对新版重新核对兼容性。原官方镜像和其余前端包保持原样。安装包长期保存在 WSL 的 `~/edgestack/ui-releases/`，不放入包含管理员数据的 `/data` 卷。

构建产物打包时同时包含仓库中的 `scripts/install-ui.sh` 和 `deploy/server/compose.ui.yaml`，后者放在安装包根目录。迁移 Server 时复制整个 Server 配置目录内的 `ui/` 与覆盖文件。

刷新浏览器后显示中文及 EdgeStack.io。部署地址由使用者配置，例如 `https://<server-host>:9443`；项目展示名称不改变 Server 的访问地址。

浏览器保留旧资源时，Mac Chrome 使用 `Command + Shift + R` 强制刷新。更新后应核对新资源版本是否已加载。

需要撤回到原英文界面时，在 Server 配置目录保留覆盖文件副本后重建 Server：

```sh
cd "$HOME/edgestack/server"
mv compose.override.yaml compose.ui.disabled.yaml
docker compose config --quiet
docker compose up -d --pull never --no-deps portainer
```

该动作恢复官方镜像原始界面，保留原 `/data` 卷、管理员和设备配置。恢复中文时将覆盖文件移回原名并再次运行 Compose。仅回退文案版本时，重新安装私有项目目录中保留的较早版本资源即可。

## 维护文案

`locales/zh-CN.catalog.json` 为批量翻译，`locales/zh-CN.overrides.json` 为人工校对和页面验收补充，后者优先。术语及分批翻译过程见 `docs/localization/ui-source-zh-CN/`。增加文案后重新构建、执行上述检查并安装新版本。

动态 DOM 本地化通过精确匹配工作，新增上游文案会保留原文。升级前必须复核新版标记结构和资源名保护规则，并重新做页面检查。
