<div align="center">
  <h1>HRPAuth-Yggdrasil-API</h1>
  <p><strong>HRPAuth identities, ready for Minecraft.</strong></p>
  <p><a href="./README.zh-CN.md">简体中文</a></p>
</div>

HRPAuth-Yggdrasil-API brings a Yggdrasil and authlib-injector interface to Minecraft clients, backed by a running HRPAuth identity center.

It serves authentication, token refresh, server joins, profiles, and textures while linking Microsoft and Mojang accounts.

## Why HRPAuth-Yggdrasil-API

- **One account backbone.** Use HRPAuth credentials across compatible game clients.
- **Standard client flow.** Support auth, refresh, join, and profile operations through Yggdrasil.
- **Connect existing identities.** Bind Microsoft and Mojang accounts to ecosystem profiles.
- **Carry profiles and textures.** Serve player profile data and skin and cape textures.
- **Extend the portal.** Include an embedded WebUI SDK for HRPAuth-hosted pages.

## Learn more

- [HA Wiki](https://wiki.mcnb.dev/) — ecosystem documentation
- [Docker image](https://hub.docker.com/r/limeight/ha-hrpauth-yggdrasil-api) — published container image
- [HRPAuth](https://github.com/CoreMatch/HRPAuth) — identity backend
- [HA-Contract](https://github.com/CoreMatch/HA-Contract) — ecosystem API contracts

## Compatibility

Yggdrasil and authlib-injector support does not imply universal compatibility with every Minecraft client or launcher.

## License & provenance

[AGPL-3.0](LICENSE). Source: [CoreMatch/HRPAuth-Yggdrasil-API](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API).
