# MDIS 运维平台

MDIS 提供资源监控、PromQL 自定义监控、告警、日志和链路查询。平台各组件共用一个镜像，启动时通过 `ROLE` 选择运行角色。

## 构建镜像

从仓库根目录构建 Linux x86-64 镜像。Dockerfile 会在构建阶段安装依赖并编译前后端。

```sh
docker build --platform linux/amd64 -t ops-mdis:latest .
```

在阿里云 ACR 绑定 GitHub 代码源后，构建规则选择仓库分支，构建上下文目录填 `/`，Dockerfile 文件名填 `Dockerfile`。构建需要访问公共镜像、依赖仓库及 GitHub Releases。网络受限时可使用 ACR 的海外构建或海外源加速。

部署时参考 [Compose 示例](plugin/allinone/ops.yaml.example) 或 [Kubernetes 清单](plugin/allinone/k8s-cluster/README.md)，将清单中的镜像替换为本次构建的镜像地址及标签。
