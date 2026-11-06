> 来源：HA-Contract@bab2a05（2026-11-07），原路径 docs/dev/HRPAuth/feature-flags.md（契约仓知识库回迁）。

# Feature Flags

Feature flags for the Yggdrasil protocol layer, corresponding to `yggdrasil.feature_flags.*` configuration items. Since the 2026-10 service split, these flags live in the configuration of the [`HRPAuth-Yggdrasil-API`](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API) service.

> For HRPAuth's own switches (`security.*`), see
> [HRPAuth Configuration](https://wiki.mcnb.dev/projects/hrpauth/configuration) in the Wiki.

| Configuration Item | Default | Description |
|--------------------|---------|-------------|
| `non_email_login` | `true` | Allows login via **Minecraft profile name** (in addition to email) at `POST /authserver/authenticate`. Implementation in [`services/auth_service.go::VerifyCredentials`](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API/blob/main/services/auth_service.go); when `true`, users can input `email` or `profiles.name`. Only affects Yggdrasil endpoints; site-side login goes through the OAuth2 flows. |
| `legacy_skin_api` | `true` | Enables the legacy skin API (`GET /skins/MinecraftSkins/{username}.png`). See [`legacy-skin-api.md`](0012-legacy-skin-api.md). When `false`, returns `404 NotFoundException`. |
| `no_mojang_namespace` | `false` | Disables Mojang namespace. When enabled, the namespace of profile properties will not have the `minecraft:` prefix. |
| `enable_mojang_anti_features` | `false` | Enables Mojang anti-cheat features. Specific behavior is interpreted by the client. |
| `enable_profile_key` | `false` | Enables the Minecraft 1.19+ Profile Key feature. When `true`, `POST /minecraftservices/player/certificates` and `GET /minecraftservices/publickeys` become available. Key pair is RSA 2048, persisted in `profile_keys`, valid 48h. See [`profile-keys.md`](0013-profile-keys.md). |
| `username_check` | `true` | Enables username checking (restricts Minecraft profile name format). **Highly recommended to keep `true`**. |
| `enable_ip_check` | `false` | Enables IP validation. When enabled, `GET /sessionserver/session/minecraft/hasJoined` will check if `query.ip` matches the IP in the session record; if not, it will be rejected (returns 204). |

## Position in Responses

Some flags are returned as-is in the `meta` section of `GET /` (Yggdrasil metadata):

```json
{
  "meta": {
    "serverName": "HRPAuth",
    "implementationName": "HRPAuth",
    "implementationVersion": "1.0.0"
  },
  "feature.non_email_login": false,
  "feature.legacy_skin_api": false,
  "feature.no_mojang_namespace": false,
  "feature.enable_mojang_anti_features": false,
  "feature.enable_profile_key": false,
  "feature.username_check": true
}
```

> The specific returned fields depend on the implementation and can be viewed in [`controllers/yggdrasil_controller.go`](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API/blob/main/controllers/yggdrasil_controller.go).
