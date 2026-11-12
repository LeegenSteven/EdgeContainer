# EdgeStack.io 简体中文界面

适用版本为镜像锁定清单中的 Portainer CE **2.45.3**。本目录提供独立的中文文案和品牌展示层，原始后端、上游 JavaScript 包、公共 API、镜像引用与设备身份均保留原样。

当前覆盖 Docker、全局管理和边缘计算的固定界面文案；Kubernetes/Azure 专有页面需要在启用对应环境时补充。代码、命令、日志、表单实际值与资源名称不通过词表翻译。帮助链接仍指向对应的上游文档。版权和许可文件保留，页脚注明基于 Portainer 社区版。

## 构建与验证

从正在运行、使用固定镜像的 Server 中取得**公开静态文件**，不需要管理员密码或 Edge 凭据：

```sh
docker cp edgestack-server:/public/index.html /tmp/upstream-index.html
```

已有覆盖包时使用首次安装保留的 `$HOME/edgestack/ui-backups/upstream-index.html`。然后在仓库根目录构建到新的空目录：

```sh
python3 scripts/build-ui.py --index /tmp/upstream-index.html --output /tmp/edgestack-ui-release
node scripts/test-ui.cjs /tmp/edgestack-ui-release
```

测试需要 Node.js、Playwright 及其 Chromium；使用系统 Chrome 时设置 `EDGESTACK_TEST_BROWSER=chrome`。测试使用临时独立配置，不读取已有浏览器会话。

产物包括 `index.html`、`edgestack/` 下带内容版本的 JS/CSS、SVG 标识和 `manifest.json`。清单记录所有产物校验和、原始入口页校验和及适用镜像。

## 安装与回退

将产物目录及 `scripts/install-ui.sh` 传到 Server 的 WSL 私有项目目录，在 WSL 执行：

```sh
bash install-ui.sh "$HOME/edgestack/ui-releases/<版本>" edgestack-server --persistent
```

安装器核对镜像和文件校验和，保留上一个入口页，将公开资源复制到 `~/edgestack/server/ui/`，并将 `deploy/server/compose.ui.yaml` 安装为配置目录内的 `compose.override.yaml`。仅对原 Server 服务运行 Compose，原命名数据卷保持挂载。首次启用需要重建 Server，网页可能要求原 admin 重新登录；后续同样挂载结构下更新文案不重启进程。

入口页和资源目录以只读 bind mount 加载，容器重启、重建和 Windows 重启后都保留。升级 Portainer 前先针对新版重新核对兼容性。原官方镜像和其余前端包保持原样。安装包长期保存在 WSL 的 `~/edgestack/ui-releases/`，不放入包含管理员数据的 `/data` 卷。

构建产物打包时同时包含仓库中的 `scripts/install-ui.sh` 和 `deploy/server/compose.ui.yaml`，后者放在安装包根目录。迁移 Server 时复制整个 Server 配置目录内的 `ui/` 与覆盖文件。

刷新浏览器后显示中文及 EdgeStack.io。访问你配置的 Server 地址，例如 `https://SERVER_HOST:9443`，品牌名称不代表已注册或绑定同名域名。

浏览器保留旧资源时，Mac Chrome 使用 `Command + Shift + R` 强制刷新。

需要撤回到原英文界面时，在 Server 配置目录保留覆盖文件副本后重建 Server：

```sh
cd "$HOME/edgestack/server"
mv compose.override.yaml compose.ui.disabled.yaml
docker compose config --quiet
docker compose up -d --pull never --no-deps portainer
```

该动作恢复官方镜像原始界面，保留原 `/data` 卷、管理员和设备配置。恢复中文时将覆盖文件移回原名并再次运行 Compose。仅回退文案版本时，重新安装私有项目目录中保留的较早版本资源即可。

## 维护文案

`locales/zh-CN.catalog.json` 为批量翻译，`locales/zh-CN.overrides.json` 为人工校对和页面验收补充，后者优先。增加文案后重新构建、执行上述检查并安装新版本。

动态 DOM 本地化通过精确匹配工作，新增上游文案会保留原文。升级前必须复核新版标记结构和资源名保护规则，并重新做页面检查。
