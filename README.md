# MDIS 运维平台

MDIS 提供资源监控、PromQL 自定义监控、告警、日志和链路查询。平台各组件共用一个镜像，启动时通过 `ROLE` 选择运行角色。

## 构建镜像

从仓库根目录构建 Linux x86-64 镜像。Dockerfile 会在构建阶段安装依赖并编译前后端。

```sh
docker build --platform linux/amd64 -t ops-mdis:latest .
```

在阿里云 ACR 绑定 GitHub 代码源后，构建规则选择仓库分支，构建上下文目录填 `/`，Dockerfile 文件名填 `Dockerfile`。基础镜像和各 exporter 从 Docker Hub 和 Kubernetes 官方仓库拉取，前端和后端编译需要访问各自的依赖仓库。网络受限时，在 ACR 个人版开启「海外机器构建」，或在企业版开启「海外源智能加速」。

部署时参考 [Compose 示例](plugin/allinone/ops.yaml.example) 或 [Kubernetes 清单](plugin/allinone/k8s-cluster/README.md)，将清单中的镜像替换为本次构建的镜像地址及标签。
