> 来源：HA-Contract@bab2a05（2026-11-07），原路径 docs/dev/HRPAuth/legacy-skin-api.md（契约仓知识库回迁）。

# Legacy Skin API

Old Minecraft clients (and a handful of legacy skin tools) request skins directly via `GET /skins/MinecraftSkins/{username}.png` instead of going through the Yggdrasil `hasJoined` + property flow.

The authlib-injector wiki describes this as the **Legacy Skin API**:

> `feature.legacy_skin_api` — Boolean, indicates whether the verification server supports the legacy skin API, i.e., `GET /skins/MinecraftSkins/{username}.png`. When `false` (default), authlib-injector serves the request locally via its built-in HTTP server; when `true`, the request is forwarded to the verification server.

## Endpoint

`GET /skins/MinecraftSkins/:username`

- **Auth**: none. The endpoint is intentionally unauthenticated so legacy clients can fetch skins without an active session.
- **Gate**: requires `yggdrasil.feature_flags.legacy_skin_api == true`. When disabled, returns `404 NotFoundException` with `errorMessage: "Legacy skin API is disabled."`.
- **Lookup order**: resolves `:username` to a `profiles.name`, then to the stored SKIN texture URL, then serves the on-disk PNG file.

### Behavior

| Condition | Response |
|-----------|-----------|
| Profile with name `:username` exists and has a SKIN texture | `200 OK`, `Content-Type: image/png`, body = PNG bytes from `<yggdrasil.server.textures_storage>/textures/<hash>` |
| Profile or SKIN texture not found | `204 No Content` (per the Yggdrasil "soft fail" convention) |
| `:username` empty after stripping `.png` | `400 BadRequestException` |
| `legacy_skin_api` disabled | `404 NotFoundException` |

### URL Stripping

The handler accepts both `/skins/MinecraftSkins/notch` and `/skins/MinecraftSkins/notch.png` — the trailing `.png` is stripped before lookup. This matches how clients in the wild issue requests.

## Implementation Notes

- Storage location is read from `yggdrasil.server.textures_storage` (default `./`).
- The resolved texture file path is `<storage>/textures/<hash>`, where `<hash>` is the SHA-256 of the validated PNG (computed and persisted by [`TextureService.SaveTexture`](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API/blob/main/services/texture_service.go)).
- The endpoint never serves a non-PNG file: `Content-Type: image/png` is set unconditionally and the PNG validity is enforced upstream by `ValidateTexture`.

## Related Code

- [`services/texture_service.go::GetTextureByHash`](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API/blob/main/services/texture_service.go) — hash → texture bytes resolver（name→path 解析现内联于 `controllers/yggdrasil_controller.go::LegacySkin`）
- [`controllers/yggdrasil_controller.go::LegacySkin`](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API/blob/main/controllers/yggdrasil_controller.go) — handler
- [`main.go`](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API/blob/main/main.go) — route registration