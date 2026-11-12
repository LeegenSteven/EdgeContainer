# EdgeStack 家庭端侧管理 PRD

## 摘要

首版把现有的 Portainer Server、Edge Agent 和容器部署流程整合成一套可复现的家庭局域网部署。用一台现有主机做 Server 中枢，两台 RK3588 做 Worker 终端，让维护者在中枢网页上接入两台设备、部署容器并查看运行状态。

## 背景

GitHub 仓库 `LeegenSteven/EdgeContainer` 的 `main` 当前以 Portainer Server 和 Agent `2.45.3` 为基线。源码已有 Docker Edge 环境接入、设备心跳和最近签到、Edge Stacks 下发及容器状态查看。首版的工作是配置、部署和验证这些能力在三台实机上的完整链路。 [源码基线](https://github.com/LeegenSteven/EdgeContainer/commit/e6379a14aef30635721ef9de3a43b11ead5f198d)

## 目标与验收

首版发布前，在同一局域网的三台实机上通过以下验收。

| 验收项          | 通过条件                                                                                                                  |
| ------------ | --------------------------------------------------------------------------------------------------------------------- |
| 两台 Worker 接入 | 主机上的 Server 网页可访问。两台 RK3588 各自运行 Docker 和 Edge Agent，分别注册到中枢。现有设备列表能区分两台 Worker，并显示各自的心跳状态和最近签到时间。                    |
| 下发与运行        | 将两台 Worker 加入同一个 Edge Group，用 Edge Stacks 下发一份使用现成 `linux/arm64` 镜像的 Compose 配置。在中枢分别确认部署成功、容器正在运行，再用容器日志或服务响应核对实际输出。 |
| 断连与恢复        | 断开其中一台 Worker 的网络，按现有心跳超时规则刷新设备列表，确认该设备显示 `Down`，另一台仍可查看容器状态。恢复网络后，掉线设备在原条目下恢复 `Heartbeat`，最近签到时间更新，容器状态可再次查看。        |
|              |                                                                                                                       |

## 目标用户

首版面向负责部署和维护家庭终端的人，先由维护者使用现有主机和两台 RK3588 完成联调。使用者需要能在 Linux 设备上安装 Docker，并配置局域网连接。

## 价值主张

- **谁**　负责部署和维护两台或更多家庭终端的人。
- **为什么**　想在一个入口确认每台设备是否已连接、部署是否成功、容器是否在运行。
- **现在怎么做**　以分别登录终端、执行 Docker 命令和查看日志作为试点对照。
- **怎么解决**　由一台 Server 管理两台 Worker，复用现有网页、Edge Agent 和容器部署流程。
- **用完有什么变化**　维护者可以从中枢完成两台 Worker 的部署和状态检查，并定位是哪台设备掉线。
- **有哪些替代**　可用 SSH 和脚本逐台部署，也可自行配置原版 Portainer。EdgeStack 首版交付针对这三台设备验证过的配置和部署步骤。

**维护者用一套可复现的部署步骤，把两台 RK3588 接到家庭中枢，并在同一个网页上管理两台设备的容器。**

## 方案

### 用户流程

打开 Server 网页 → 分别接入两台 RK3588 → 确认设备心跳和最近签到 → 通过 Edge Stacks 下发容器 → 查看各设备的部署结果、容器状态和运行输出。

### 首版整合

| 位置 | 部署与复用方式 |
| --- | --- |
| 现有主机 · Server | 运行 Portainer Server，启用 Edge Compute，在局域网提供网页、Edge Agent 签到和按需隧道。持久化 Server 数据，将地址、镜像版本和数据目录写入部署配置，便于以后换到 SoC 板。 |
| 两台 RK3588 · Worker | 每台运行 Docker 和 ARM64 版 Edge Agent，使用 Edge Agent Standard 各自接入 Server，由 Agent 在本机部署容器。 |
| 中枢网页 | 用现有环境列表查看连接状态和签到时间，用 Edge Stacks 查看各设备的部署结果，再进入对应环境查看容器状态和日志。 |

[接入向导源码](https://github.com/LeegenSteven/EdgeContainer/tree/e6379a14aef30635721ef9de3a43b11ead5f198d/portainer/app/react/portainer/environments/wizard/EnvironmentsCreationView/WizardDocker) · [设备列表源码](https://github.com/LeegenSteven/EdgeContainer/blob/e6379a14aef30635721ef9de3a43b11ead5f198d/portainer/app/react/portainer/HomeView/EnvironmentList/EnvironmentItem/EnvironmentCard.tsx) · [Edge Stacks 源码](https://github.com/LeegenSteven/EdgeContainer/tree/e6379a14aef30635721ef9de3a43b11ead5f198d/portainer/app/react/edge/edge-stacks)

### 家庭事件的后续接入设计

家庭事件由 Worker 上的业务容器产生，例如一次观察或动作。后续由 Server 上独立的业务模块负责接收、存储和查询，网页增加独立的「家庭事件」入口。事件用 Server 内现有的环境 ID（`EndpointID`）关联来源设备。设备签到、部署状态和容器日志继续走 Portainer 的运维链路。首版把这个职责边界记入设计，事件接口、存储和页面随家庭事件功能一起实现。 [环境标识源码](https://github.com/LeegenSteven/EdgeContainer/blob/e6379a14aef30635721ef9de3a43b11ead5f198d/portainer/api/portainer.go#L706-L707)

### 联调前待确认

- 核对主机架构、两台板的 Linux 和 Docker 版本，以及 Server 与 Agent 镜像在对应设备上能否运行。仓库已有 Agent 的 `linux-arm64` 构建目标，RK3588 上的运行结果以实测为准。 [Agent 构建脚本](https://github.com/LeegenSteven/EdgeContainer/blob/e6379a14aef30635721ef9de3a43b11ead5f198d/agent/release.sh#L60-L61)
- 当前源码包含 Server 和 Agent，验收用的 ARM64 容器在联调前选定，并固定镜像版本和预期输出。如手头已有可在两台板上运行的推理镜像，可直接用它验证同一条链路。

## 发布计划

先启动 Server 并接入第一台 Worker，再接入第二台，完成容器下发、状态检查和断连恢复。三项实机验收通过后，交付三台设备的部署配置、操作步骤和验收记录。家庭事件、模型版本管理与升级、云端问答，以及 Server 迁移到 SoC 板，留待后续版本评估。
