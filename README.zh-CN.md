<div align="center">
  <h1>HRPAuth-Yggdrasil-API</h1>
  <p><strong>让 HRPAuth 身份畅行 Minecraft。</strong></p>
  <p><a href="./README.md">English</a></p>
</div>

HRPAuth-Yggdrasil-API 以运行中的 HRPAuth 身份中心为后端，为 Minecraft 客户端提供 Yggdrasil 与 authlib-injector 接口。

它承载认证、令牌刷新、服务器加入、档案与纹理，并支持绑定 Microsoft 和 Mojang 账号。

## 为什么选择 HRPAuth-Yggdrasil-API

- **共用一套账号根基。** 在兼容的游戏客户端中使用 HRPAuth 凭据。
- **标准客户端流程。** 通过 Yggdrasil 支持认证、刷新、加入与档案操作。
- **连接既有身份。** 将 Microsoft 和 Mojang 账号绑定到生态档案。
- **随行档案与纹理。** 提供玩家档案数据及皮肤、披风纹理。
- **扩展账号门户。** 内嵌用于 HRPAuth 托管页面的 WebUI SDK。

## 进一步了解

- [HA Wiki](https://wiki.mcnb.dev/) — 生态文档
- [Docker 镜像](https://hub.docker.com/r/limeight/ha-hrpauth-yggdrasil-api) — 已发布的容器镜像
- [HRPAuth](https://github.com/CoreMatch/HRPAuth) — 身份后端
- [HA-Contract](https://github.com/CoreMatch/HA-Contract) — 生态 API 契约

## 兼容性

支持 Yggdrasil 与 authlib-injector，并不意味着兼容所有 Minecraft 客户端或启动器。

## 许可证与来源

[AGPL-3.0](LICENSE)。来源：[CoreMatch/HRPAuth-Yggdrasil-API](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API)。
