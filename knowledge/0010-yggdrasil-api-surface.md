> 来源：HA-Contract@bab2a05（2026-11-07），原路径 docs/dev/HRPAuth/yggdrasil-api.md（契约仓知识库回迁）。

# Yggdrasil API Reference

This document is the single source of truth for the **Yggdrasil-compatible**
HTTP surface exposed by the **HRPAuth-Yggdrasil-API** service, plus the
service's current-profile texture management wrappers used by first-party UI
flows. This service is decoupled from the HRPAuth core and focuses entirely on
Minecraft-related logic.

> Reference target: [authlib-injector Yggdrasil Server Technical Specification](https://yushijinhun.github.io/authlib-injector/) and [wiki.vg Authentication](https://wiki.vg/Authentication).

## Contents

1. [Conventions](#1-conventions)
2. [Errors](#2-errors)
3. [Meta — `GET /`](#3-meta--get-)
4. [Authserver](#4-authserver)
5. [Sessionserver](#5-sessionserver)
6. [Profile and Texture Management](#6-profile-and-texture-management)
7. [Legacy Skin API](#7-legacy-skin-api)
8. [Minecraft Services (Profile Keys)](#8-minecraft-services-profile-keys)
9. [Account Registration and Sync](#9-account-registration-and-sync)

---

## 1. Conventions

- All requests and responses are JSON, `Content-Type: application/json; charset=utf-8`. The two texture download endpoints (`GET /textures/:hash`, `GET /skins/MinecraftSkins/:username`) return `image/png` instead.
- UUIDs in paths and responses are **unhyphenated lowercase hex** (32 chars).
- All endpoints should be served over HTTPS in production.

## 2. Errors

Standard Yggdrasil error envelope:

```json
{
  "error":        "<machine-readable type>",
  "errorMessage": "<human-readable detail>",
  "cause":        null
}
```

| HTTP | `error` | Meaning |
|------|---------|---------|
| 400 | `BadRequestException` | Malformed request (missing fields, bad query params, etc.). |
| 400 | `IllegalArgumentException` | Generic argument rejection. |
| 401 | `UnauthorizedOperationException` | Texture upload/delete with no/invalid `Authorization: Bearer <accessToken>`. |
| 403 | `ForbiddenOperationException` | Wrong credentials, invalid token, rate limited, or feature disabled. |
| 404 | `NotFoundException` | `GET /textures/:hash` hash not on disk. |
| 404 | `ProfileNotFoundException` | `GET /sessionserver/session/minecraft/profile/:uuid` could not find the UUID. |
| 404 | `MethodNotAllowedException` | Wrong HTTP method for a known path. |

The `/minecraftservices/player/certificates` endpoint additionally emits a **Mojang-style** error envelope (`path` + `errorType` + `error` + `errorMessage`) for the `401 Unauthorized` case to match what the Minecraft client expects.

## 3. Meta — `GET /`

| Status | Headers | Body |
|--------|---------|------|
| `200` | `X-Authlib-Injector-API-Location: /` | See below |

```json
{
  "status":  "online",
  "success": true,
  "message": "HRPAuth Yggdrasil API Provider is running.",
  "site": {
    "name":        "<site.name>",
    "url":         "<runtime site URL pulled from HRPAuth core>",
    "version":     "<site.version>",
    "server_time": "<YYYY-MM-DD HH:MM:SS>"
  },
  "yggdrasil": {
    "meta": {
      "serverName":            "<yggdrasil.server.name>",
      "implementationName":    "<yggdrasil.server.implementation>",
      "implementationVersion": "<yggdrasil.server.version>",
      "links": {
        "homepage": "<runtime frontend URL when available, else yggdrasil.server.links.homepage>",
        "register": "<runtime frontend URL + /register when available, else yggdrasil.server.links.register>"
      },
      "feature.non_email_login":             true,
      "feature.legacy_skin_api":             true,
      "feature.no_mojang_namespace":         false,
      "feature.enable_mojang_anti_features": false,
      "feature.enable_profile_key":          false,
      "feature.username_check":              true
    },
    "skinDomains":        ["<domain>", ".<domain>"],
    "signaturePublickey": "<PEM public key loaded from yggdrasil.server.signature_public_key_path>"
  }
}
```

The Yggdrasil metadata is nested under the `yggdrasil` key; the top level
additionally carries `status`, `success`, `message`, and `site`. The core
service merges this whole `yggdrasil` object into its own `GET /` response via
presence `root_metadata`.

`skinDomains` defaults to `[<site URL domain>, .<site URL domain>]` (derived
from the runtime site URL pulled from HRPAuth core) when
`yggdrasil.server.skin_domains` is empty.

The response also carries `X-Authlib-Injector-API-Location: /` so that authlib-injector ALI resolution points back at the same root.

## 4. Authserver

| Endpoint | Auth | Notes |
|----------|------|-------|
| `POST /authserver/authenticate` | none | Username/password + `agent`. Body: `{username, password, agent:{name,version}, clientToken?, requestUser?}`. Rate-limited by `security.rate_limit_*`. |
| `POST /authserver/refresh` | none | Body: `{accessToken, clientToken, selectedProfile?, requestUser?}`. **Response no longer includes `availableProfiles`** — only `accessToken`, `clientToken` and `selectedProfile`. `requestUser` is accepted but the current implementation never returns a `user` object. |
| `POST /authserver/validate` | none | Body: `{accessToken, clientToken?}`. Returns `204 No Content` on success, `403` otherwise. |
| `POST /authserver/invalidate` | none | Body: `{accessToken, clientToken?}`. Returns `204` after invalidating; an unparseable JSON body returns `403 ForbiddenOperationException`. |
| `POST /authserver/signout` | none | Body: `{username, password}`. **Rate-limited** with the same Redis counter as `/authenticate` to prevent password enumeration via this endpoint. Always returns `204`. |

### Successful `/authenticate` Response

```json
{
  "accessToken": "<new or reused>",
  "clientToken": "<echoed or generated>",
  "availableProfiles": [
    { "id": "<uuid>", "name": "<profile name>" }
  ],
  "selectedProfile": { "id": "<uuid>", "name": "<profile name>" }
}
```

`user` is appended when `requestUser: true`.

## 5. Sessionserver

| Endpoint | Notes |
|----------|-------|
| `POST /sessionserver/session/minecraft/join` | Body: `{accessToken, selectedProfile, serverId}`. Records a session keyed by `(profile_id, server_id)` with TTL = `yggdrasil.security.session_expiry_seconds`. |
| `GET /sessionserver/session/minecraft/hasJoined?username=&serverId=&ip=` | Returns `204` on miss; otherwise a profile object including `textures` and (when applicable) the `textures` property signature. `ip` is only enforced when `yggdrasil.feature_flags.enable_ip_check = true`. |
| `GET /sessionserver/session/minecraft/profile/:uuid?unsigned=true` | Returns the profile plus properties; `signature` is included only when `unsigned=false`. Misses return `404 ProfileNotFoundException`. A lowercase alias route `GET /sessionserver/session/minecraft/hasjoined` is also registered for `hasJoined`. |

`hasJoined` response:

```json
{
  "id":   "<uuid>",
  "name": "<profile name>",
  "properties": [
    {
      "name":  "textures",
      "value": "<base64 JSON>",
      "signature": "<base64>"
    }
  ]
}
```

## 6. Profile and Texture Management

| Endpoint | Auth | Notes |
|----------|------|-------|
| `POST /api/profiles/minecraft` | none | Body: 纯 JSON 数组 `["name1","name2",...]`（authlib-injector / wiki.vg 标准）。响应为纯数组 `[{"id","name"}]`，仅返回命中的档案；未命中任何档案时返回 `[]`。非法请求体返回 `IllegalArgumentException`。 |
| `POST /texture/get` | `Authorization: Bearer <accessToken>` | Returns the caller's manageable profiles and each profile's currently active texture bindings. This response is **current-state only** and does not include per-account texture history. |
| `POST /texture/upload/:uuid` | `Authorization: Bearer <accessToken>` | WebUI-facing wrapper for current profile texture management. Multipart with `file` (PNG) and form fields `type ∈ {skin,cape}` plus optional `model=default|slim` for skins. Stores the original PNG, regenerates the profile `textures` property, and generates a WebP preview for the management UI. Non-standard but proportional dimensions may yield warnings in the JSON response. |
| `DELETE /texture/delete/:uuid/:textureType` | `Authorization: Bearer <accessToken>` | Removes the currently active `skin` or `cape` binding from the target profile and re-signs the remaining payload if needed. This is a profile-state operation, not texture-library asset deletion. |
| `GET /texture/avatar/:uuid` | none | Returns the current local skin's front face and head overlay as `image/png`; existing skins are rendered on first access. Returns `400` for an invalid UUID, `404` when no local skin is bound, and `500` for storage or rendering errors. This does not change the account avatar. |
| `GET /textures/:hash` | none | Streams `<storage>/textures/<hash>` as `image/png`. `404 NotFoundException` if the file is missing. |
| `GET /previews/:fileName` | none | Streams the generated WebP preview file used by the profile texture management UI. |

Skin uploads generate a face avatar. On deletion or replacement, the old
texture and avatar are eligible for background cleanup after seven days, only
when no active or unexpired binding references the same texture.

The standard write endpoints `PUT/DELETE /api/user/profile/:uuid/:textureType`
are not exposed directly by the current service implementation. WebUI-facing
texture writes are provided by the business wrappers above, while the Yggdrasil
read surfaces remain standard (`/sessionserver/...`, `/textures/:hash`).

## 7. Legacy Skin API

`GET /skins/MinecraftSkins/:username` — see [`legacy-skin-api.md`](0012-legacy-skin-api.md) for full details.

- Gate: `yggdrasil.feature_flags.legacy_skin_api == true`.
- Trailing `.png` is stripped from `:username` before lookup.
- Returns the on-disk SKIN PNG or `204 No Content`.

## 8. Minecraft Services (Profile Keys)

`POST /minecraftservices/player/certificates` and `GET /minecraftservices/publickeys` — see [`profile-keys.md`](0013-profile-keys.md) for full details.

- Gate: `yggdrasil.feature_flags.enable_profile_key == true`.
- `/player/certificates` auth: `Authorization: Bearer <yggdrasil accessToken>`.
- 2048-bit RSA key pair, persisted per user, valid 48h with refresh window at +40h.
- Public key signed by the Yggdrasil signature private key (SHA1withRSA), so the existing `signaturePublickey` from `GET /` doubles as the trust root.

## 9. Account Registration and Sync

### Game Account Registration (`POST /register`)

Creates a game-specific account and default profile for an existing core user.

- **Auth**: None (requires core credentials in body).
- **Body**: `{ "identifier": "<email or username>", "password": "<password>", "mojang_uuid": "<optional>" }`.
- **Logic**:
    1. Verifies credentials against the HRPAuth core service.
    2. Checks if a game account already exists for the user (1:1 mapping).
    3. Inherits the `username` from the core service for the default profile.
    4. Binds `mojang_uuid` if provided and valid.

### Data Consistency (Lazy Loading)

The Yggdrasil service no longer receives push events for username changes or account deletions. Instead, it follows a **lazy loading** pattern:

1.  **Cache Check**: When a profile name is needed (e.g., during `/authenticate` or `/sessionserver` calls), the service checks Redis for a cached `username` associated with the `core_user_id`.
2.  **Pull from Core**: If not cached, the service calls HRPAuth Core's `GET /services/users/:uuid` internal API.
3.  **Update & Cache**: The retrieved name is stored in Redis (TTL: 10 minutes) and the local database is updated to reflect the change.
4.  **Auto-Cleanup**: If HRPAuth Core returns a `404 Not Found`, the Yggdrasil service assumes the core account has been deleted and triggers local account deletion.

### Internal Heartbeat (`GET /internal/heartbeat`)

Used by HRPAuth core to verify service health and internal key validity.

- **Auth**: `X-Internal-Key` header.
- **Response**: `200 OK` with the standard success envelope (message "I am alive") on success; `401` with `invalid_credentials` if the key is invalid.

### Proxy Registration (`POST /internal/proxy-register`)

Available for creating a game account without an HRPAuth core identity.

- **Auth**: `X-Internal-Key` header.
- **Body**: `{ "username": "<string>", "password": "<string>", "mojang_uuid": "<string>" }`.
- **Response**: `200` with `data: {account_id, profile_id}`; `400` for an invalid body; `401` for an invalid internal key; `409 username_already_bound` on a non-bindable username collision; `500` on failure.
- **Logic**:
    1. `mojang_uuid` already linked to an account → idempotent `200` with the existing account/profile.
    2. Username free → creates an account with `cbh = 0` (Created By Human = false), stores the provided `password` (hashed) for local Yggdrasil auth. Account is subject to bot cleanup if not claimed within 30 days.
    3. Username collides with an account that has no `mojang_uuid`:
        - `mbe = 1` → **completes the Mojang bind window** opened by the account owner via `POST /player/mojang-bind-enable`: sets `mojang_uuid`, refreshes `last_sign_at`, resets `mbe = 0`, cancels the pending 15-minute revert. `cbh`, `password`, `core_user_id` and the profile are preserved; the supplied `password` is discarded. Responds `200` with the existing account's `account_id`/`profile_id`.
        - `mbe = 0` → `409 username_already_bound` (HA accounts have priority; a Mojang player cannot take the name).
    4. Username collides with an account already bound to a different `mojang_uuid` → `409 username_already_bound` regardless of `mbe`.

> Current WinnerProxy behavior: WinnerProxy does not call this endpoint. Its
> proxy registration issues `POST /register` against the upstream with an
> OAuth2 Bearer token and body `{username, password, email: "",
> mojang_uuid}`. Upstream `/register` requires the `identifier` field, so
> this outbound call does not succeed against the current upstream as of
> 2026-11-04 (the contract-sanctioned endpoint is
> `POST /internal/proxy-register`, see above). The batch profile query used
> to target `POST {upstream}/yggdrasil/api/profiles/minecraft` (an extra
> `/yggdrasil` segment this contract does not define) and 404'd; it was
> fixed on 2026-11-05 (WinnerProxy `4ded866`) to call the prefix-less
> `POST /api/profiles/minecraft` from §6, with upstream failures mapped to
> explicit 502 errors instead of a bare 500. WinnerProxy's former dead
> client methods for `POST /user/declare-email` and
> `POST /user/mojang-bind-enable` were deleted on 2026-11-05; player-side
> flows use the Velocity plugin's `/admin/claim-user` and
> `/admin/force-bind` paths instead (see
> `openapi/hrpauth-yggdrasil-business.yaml`).
