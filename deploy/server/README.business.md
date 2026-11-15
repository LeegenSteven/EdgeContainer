# 用户工作台 Server 构建与发布

业务模块直接编入本仓库 Portainer Server。原管理员 API 沿用固定 2.45.3 基线，原生前端从本仓库完整编译并增加业务管理页面，新业务数据单独存放在 `/data/edgestack/business.db`。

## 构建

先使用 Node 22.22.1、pnpm 10.26.2 编译原生前端，再使用与 `portainer/go.mod` 一致的 Go 工具链构建后端：

```sh
scripts/build-native-ui.sh
scripts/build-business-server.sh /tmp/edgestack-business-context portainer/dist/public
```

在可用 Docker 的 Server 主机执行：

```sh
docker build --pull=false -t edgestack-server:business /tmp/edgestack-business-context
docker image inspect edgestack-server:business --format '{{.Id}}'
```

将返回的完整 `sha256:…` 用作 `SERVER_IMAGE`，同时传给界面构建：

```sh
python3 scripts/build-ui.py --index portainer/dist/public/index.html --native-public portainer/dist/public --output '<新空目录>' --server-image 'sha256:<完整镜像ID>'
```

镜像构建上下文只包含二进制、语音测试 HTML、原生 public 资产和 Dockerfile，禁止加入真实数据库、凭据或浏览器会话。构建产物只针对 linux/amd64 Server；Worker 的语音镜像固定为经验证的 arm64 包。

## 配置与发布

准备 Server 私有目录，目录 `0700`，其中 `deepseek.key`、`bootstrap.json`、`voice-upstream.token` 均为普通文件且权限 `0600`。启动登记格式与授权规则见 [业务 API 说明](../../portainer/api/edgestack/README.md)。浏览器没有管理员密钥，也不能自行提交镜像、Compose 或来源凭据。

`compose.business.yaml` 与原 `compose.yaml`、已安装的 `compose.ui.yaml` 合并，保留原 `/data` 命名卷。设置 `EDGESTACK_PRIVATE_DIR`，且 `NO_PROXY` 包含 Worker 局域网地址。若依赖出口代理，单独传入 `HTTPS_PROXY`；不得让 Worker 上游 Bearer 经出口代理。把这些 Compose 文件固定在 `COMPOSE_FILE` 中，避免以后只重建基础配置时丢失业务挂载。

首次替换前停止旧 Server，再一致复制 `/data` 并备份 `.env`、Compose 和持久界面文件。恢复一个 Server 实例后检查原管理首页、登录权限、普通用户路由、事件接收、总结和模型回执。禁止两个 Server 同时打开同一数据卷。

Worker 使用独立来源令牌，TLS 按可信 CA 或从已认证 SSH 导出的真实叶证书指纹核验。证书更新时由管理员重新核对指纹；没有跳过 TLS 验证的模式。语音代理使用另一把私有上游令牌，浏览器请求中不含该令牌。

## 回退

恢复旧 `SERVER_IMAGE`、`.env`、Compose 和原界面文件，再启动旧 Server。保留新业务数据库，避免丢失已接收记录；必要时使用发布前备份恢复原管理库。前后端回退不会替用户撤回已执行的设备模型变更；选择此前已发布包，重新预览、确认，并核对真实模型回执。运行时失败不会自动覆盖用户选择，任务操作记录保留状态。

## 管理页面与业务备份

管理员在原生侧栏的“业务管理”中登记任务与来源、编辑业务授权、换发摄取凭据、验证发布模型并查看操作与审计。资料编辑使用 `metadataVersion`，部署目标不可重绑。启动登记只补充缺失对象；更新已有配置通过差异导入预览和确认执行。

云端设置初始沿用 `DEEPSEEK_API_KEY_FILE` 主机文件。网页验证保存的新 Key 存于 `/data/edgestack/secrets/`，目录 `0700`、文件 `0600`；状态接口不回显 Key。托管密钥缺失时提示重新配置，恢复主机文件来源须显式操作。每份总结固定自己的密钥与模型快照。

原生系统备份包含业务库的一致 bbolt 快照，排除上述私有密钥文件。共享门锁协调 HTTP 变更、后台清理和查询触发的写入；正在派发的业务操作会阻止备份，等待模型回执的操作可以快照。

恢复沿用原生未初始化实例流程。恢复后来源凭据全部撤销并要求换发，活动操作及验证标记“需核对”，旧预览失效，用户、团队及设备关联再次核对，不自动重新部署。恢复保护独立于业务数据库持久保存，首次启动及后续重启均跳过旧启动登记；需要重新导入时，管理员须明确预览并确认导入配置。恢复到另一台 Server 时另行配置云端密钥、来源凭据和设备连接。私有密钥采用单独的受控备份流程。

原生编译输出与用户工作台清单共同绑定后端镜像。已挂载的 `index.html` 必须与新镜像中的 native bundle 对应；统一发布前保存原镜像、环境配置、界面目录及一致数据快照。
