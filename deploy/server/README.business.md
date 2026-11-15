# 用户工作台 Server 构建与发布

业务模块直接编入本仓库 Portainer Server。原管理员 API 和原生静态资源沿用固定 2.45.3 基线，新业务数据单独存放在 `/data/edgestack/business.db`。

## 构建

使用与 `portainer/go.mod` 一致的 Go 工具链执行：

```sh
scripts/build-business-server.sh /tmp/edgestack-business-context
```

在可用 Docker 的 Server 主机执行：

```sh
docker build --pull=false -t edgestack-server:business /tmp/edgestack-business-context
docker image inspect edgestack-server:business --format '{{.Id}}'
```

将返回的完整 `sha256:…` 用作 `SERVER_IMAGE`，同时传给界面构建：

```sh
python3 scripts/build-ui.py --index '<固定基线的原始index.html>' --output '<新空目录>' --server-image 'sha256:<完整镜像ID>'
```

镜像构建上下文只包含二进制、语音测试 HTML 和 Dockerfile，禁止加入真实数据库、凭据或浏览器会话。构建产物只针对 linux/amd64 Server；Worker 的语音运行时使用配方中固定摘要的 ARM64 上游镜像。

## 配置与发布

准备 Server 私有目录，目录 `0700`，其中 `deepseek.key`、`bootstrap.json`、`voice-upstream.token` 均为普通文件且权限 `0600`。启动登记格式与授权规则见 [业务 API 实现与类型定义](../../portainer/api/edgestack/)。来源凭据由管理员接口创建，并使用接口返回的完整令牌；私有文件及令牌不得进入构建上下文。浏览器不持有管理员密钥，也不能自行提交镜像、Compose 或来源凭据。

`compose.business.yaml` 与原 `compose.yaml`、已安装的 `compose.ui.yaml` 合并，保留原 `/data` 命名卷。设置 `EDGESTACK_PRIVATE_DIR`，且 `NO_PROXY` 包含 Worker 局域网地址。若依赖出口代理，单独传入 `HTTPS_PROXY`；不得让 Worker 上游 Bearer 经出口代理。把这些 Compose 文件固定在 `COMPOSE_FILE` 中，避免以后只重建基础配置时丢失业务挂载。

首次替换前停止旧 Server，再一致复制 `/data` 并备份 `.env`、Compose 和持久界面文件。恢复一个 Server 实例后检查原管理首页、登录权限、普通用户路由、事件接收、总结和模型回执。禁止两个 Server 同时打开同一数据卷。

Worker 使用独立来源令牌，TLS 按可信 CA 或从已认证 SSH 导出的真实叶证书指纹核验。证书更新时由管理员重新核对指纹；没有跳过 TLS 验证的模式。语音代理使用另一把私有上游令牌，浏览器请求中不含该令牌。

## 回退

恢复旧 `SERVER_IMAGE`、`.env`、Compose 和原界面文件，再启动旧 Server。保留新业务数据库，避免丢失已接收记录；必要时使用发布前备份恢复原管理库。前后端回退不会替用户撤回已执行的设备模型变更；选择此前已发布包，重新预览、确认，并核对真实模型回执。运行时失败不会自动覆盖用户选择，任务操作记录保留状态。
