<div align="center">
  <h1>Armorial Yggdrasil API</h1>
  <p><strong>让 Armorial 身份畅行 Minecraft。</strong></p>
  <p><a href="./README.md">English</a></p>
</div>

Armorial Yggdrasil API 以运行中的 Armorial 身份中心为后端，为 Minecraft 客户端提供 Yggdrasil 与 authlib-injector 接口。

它承载认证、令牌刷新、服务器加入、档案与纹理，并支持绑定 Microsoft 和 Mojang 账号。

## 为什么选择 Armorial Yggdrasil API

- **共用一套账号根基。** 在兼容的游戏客户端中使用 Armorial 凭据。
- **标准客户端流程。** 通过 Yggdrasil 支持认证、刷新、加入与档案操作。
- **连接既有身份。** 将 Microsoft 和 Mojang 账号绑定到 Armorial 生态档案。
- **随行档案与纹理。** 提供玩家档案数据及皮肤、披风纹理。
- **扩展账号门户。** 内嵌用于 Armorial 托管页面的 WebUI SDK。

## 进一步了解

- [Armorial Wiki](https://wiki.mcnb.dev/) — 生态文档
- [Docker 镜像](https://hub.docker.com/r/limeight/ha-hrpauth-yggdrasil-api) — 已发布的容器镜像
- [Armorial](https://github.com/CoreMatch/Armorial) — 身份后端
- [Armorial API Contracts](https://github.com/CoreMatch/HA-Contract) — 生态 API 契约

## 兼容性

支持 Yggdrasil 与 authlib-injector，并不意味着兼容所有 Minecraft 客户端或启动器。

## 许可证与来源

[AGPL-3.0](LICENSE)。来源：[CoreMatch/HRPAuth-Yggdrasil-API](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API)。
