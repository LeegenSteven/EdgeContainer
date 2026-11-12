# EdgeStack

基于 Portainer CE 的简体中文界面、EdgeStack.io 品牌资源与离线会议模拟器。保留原有 `portainer/`、`agent/` 源码。

| 源码 | 用途 |
| --- | --- |
| [中文界面](deploy/ui/README.md) | 文案映射、品牌标识、构建、安装与回退 |
| [会议模拟器](demo/conference/README.md) | 本地模拟 Server 和 RK3588、单文件构建与交互检查 |
| [镜像引用](deploy/images.lock.json) | 适配的官方镜像版本、平台和公开 digest |
| [界面挂载配置](deploy/server/compose.ui.yaml) | 在已有 Server 上加载只读界面资源 |

`deploy/ui/locales/` 保存中文词表，`deploy/ui/src/` 保存展示层代码，`scripts/` 保存构建与检查脚本。中文界面通过独立展示层加载，沿用原后端接口与数据卷。

会议模拟器使用生成的数据、演示凭据和示例地址，操作保存在本地浏览器中。构建所需的上游前端资源来自适配镜像的公开 `/public` 目录。HTML、ZIP、交互文档和构建记录均在本地生成，不提交到仓库。

上游版权与开源许可保留在原源码目录中，单文件构建器也会内嵌相关许可。
