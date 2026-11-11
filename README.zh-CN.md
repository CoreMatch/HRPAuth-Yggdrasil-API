# HRPAuth-Yggdrasil-API

[English](README.md)

HRPAuth-Yggdrasil-API 是 HRPAuth 生态中兼容 Yggdrasil 协议的 Minecraft 认证服务。它向游戏客户端（兼容 [authlib-injector](https://yushijinhun.github.io/authlib-injector/)）提供标准 Yggdrasil 协议接口，并连接运行中的 [HRPAuth](https://github.com/CoreMatch/HRPAuth) 核心完成账号校验与生态集成。

## 前置依赖

- Go 1.26.4+（源码构建）或 Docker
- MySQL —— 需先创建数据库与用户；数据表会在启动时自动创建
- Redis
- 运行中的 [HRPAuth](https://github.com/CoreMatch/HRPAuth) 核心 —— 启动时可选，但常规用户认证与 WebUI 集成必须连接核心；未连接时仅内部登记的代理账号可登录。

## 快速开始（二进制）

```bash
# 1. 构建
./build.sh                    # 产出 build/HAygg

# 2. 首次运行：在二进制同目录生成 config.yaml 与 RSA 签名密钥对
./build/HAygg

# 3. 停止，编辑 config.yaml（见下文"配置"），再次启动
./build/HAygg

# 4. 验证
curl http://localhost:2770/
# 返回的 JSON 中应包含 "status": "online"
```

服务默认监听 `2770` 端口（`config.yaml` 中的 `server.port`）。配置文件与生成的密钥对必须与二进制放在同一目录。

## 快速开始（Docker）

镜像已发布到 Docker Hub：[`limeight/ha-hrpauth-yggdrasil-api`](https://hub.docker.com/r/limeight/ha-hrpauth-yggdrasil-api)（`linux/amd64` + `linux/arm64` 多架构；tag `1.0.0-7ab0d29` 并列 `latest`，生效引用以 [`deploy-manifest.yaml`](deploy-manifest.yaml) 登记的 manifest list digest 为准）：

```bash
docker run -d --name yggdrasil-api \
  -p 2770:2770 \
  -v /path/to/config.yaml:/app/config.yaml \
  -v ygg-storage:/app/storage \
  limeight/ha-hrpauth-yggdrasil-api@sha256:9c78eddf66dd493aa2cdd77193ba52e5e85fcb378edeec45d0425e5a551c2f59
```

- 镜像内**不含** `config.yaml`，请将其挂载到 `/app/config.yaml`（若挂载目录可写，首次运行也可自动生成）。
- 也可以设置 `CONFIG_TEMPLATE` 与 `RENDERED_CONFIG`，在容器启动时用 envsubst 模板渲染配置（见 `docker-entrypoint.sh`）。
- 纹理文件存储在 `/app/storage`。
- 仓库 Dockerfile 保留用于本地构建（`docker build -t hrpauth-yggdrasil-api .`）；部署引用以已发布镜像为准。

## 配置（必改字段）

首次启动会自动生成默认 `config.yaml`。生产使用前通常必须修改以下字段：

| 字段 | 含义 |
|---|---|
| `core_api.base_url` | HRPAuth 核心地址，如 `http://localhost:2778` |
| `core_api.client_id` / `core_api.client_secret` | 核心服务 API 的 OAuth2 凭据 |
| `database.host` / `db_name` / `user` / `password` | MySQL 连接（数据库需已存在） |
| `redis.host` / `redis.port` | Redis 连接 |
| `microservice.relay_url` | HRPAuth 核心可访问到本服务的地址 |
| `callback.url` | 纹理 URL 使用的公网基址；留空时自动取自核心的站点元数据 |

完整字段以 [`config.yaml`](config.yaml) 与 [`config/config.go`](config/config.go) 为准。

## authlib-injector 兼容性

本服务实现 Yggdrasil 协议，兼容 authlib-injector：将 injector（或任何 Yggdrasil 客户端）指向本服务的公网地址即可。生产环境建议使用 HTTPS。`yggdrasil.server.skin_domains` 留空时默认取站点域名。

## API 契约

API 契约统一维护在 [HA-Contract](https://github.com/CoreMatch/HA-Contract)：

- Yggdrasil 协议面：[`docs/dev/HRPAuth/yggdrasil-api.md`](https://github.com/CoreMatch/HA-Contract/blob/main/docs/dev/HRPAuth/yggdrasil-api.md)
- 业务端点（OpenAPI）：[`docs/api/openapi/hrpauth-yggdrasil-business.yaml`](https://github.com/CoreMatch/HA-Contract/blob/main/docs/api/openapi/hrpauth-yggdrasil-business.yaml)

## WebUI SDK

本仓库内嵌了一份 WebUI SDK 包，服务启动时自动上传至 HRPAuth 核心。SDK 包格式与挂载契约见 [`docs/dev/HRPAuth/sdk-package.md`](https://github.com/CoreMatch/HA-Contract/blob/main/docs/dev/HRPAuth/sdk-package.md)。

## 许可

[AGPL-3.0](LICENSE)
