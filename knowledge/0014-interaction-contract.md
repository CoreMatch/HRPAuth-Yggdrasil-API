> 来源：HA-Contract@bab2a05（2026-11-07），原路径 docs/dev/HRPAuth-Yggdrasil-API/README.md（契约仓知识库回迁）。

# HRPAuth-Yggdrasil-API Interaction Contract

This document defines the **External Interaction Contract** for the `HRPAuth-Yggdrasil-API` service. It focuses exclusively on how this service interacts with Minecraft clients, HRPAuth Core, and other ecosystem components.

---

## 1. API Surface & Protocols

### 1.1 Yggdrasil Protocol (Minecraft Standard)
This service is the authoritative provider for Yggdrasil-compatible endpoints.
- **Surface**: `/authserver/*`, `/sessionserver/*`, `/api/profiles/*`, `/textures/*`.
- **Reference**: Full endpoint details are in [yggdrasil-api.md](0010-yggdrasil-api-surface.md).
- **UUID Constraint**: All UUIDs in paths and JSON bodies MUST be **32-character, unhyphenated, lowercase hex**.

### 1.2 HA Business API (Unified Standard)
All non-Yggdrasil endpoints (e.g., `/texture/get`, `/texture/upload/:uuid`, `/register`, `/internal/*`) MUST follow the **HA Unified Response Format**:
```json
{
  "success": true,
  "message": "...",
  "data": { ... },
  "code": "error_code",
  "meta": { "request_id": "..." }
}
```
- **Error Codes**: Use stable `lower_snake_case` codes registered in `docs/api/error-codes.md`.
- **Tracing**: Every response MUST include a `request_id` in the `meta` field.

---

## 2. HRPAuth Core Integration

### 2.1 Microservice Handshake
The service integrates with HRPAuth Core via the **Microservice Extension Layer**:
- **Presence**: Registers via `POST /services/presence` with `security_level: 0`, scope name `yggdrasil-api`, `frontend_areas: ["webui-dash"]`, and a `root_metadata.yggdrasil` block that the core merges into its `GET /` response.
- **SDK Aggregation**: Delivers its frontend integration (Texture Management, Mojang Binding) as a source package via `POST /services/sdk-packages`.
- **Relay**: Declares relay rules via `POST /services/relay` to expose business endpoints through the HRPAuth Core gateway.

### 2.2 Authentication & Security
- **Inbound Auth (from Core)**: Internal callbacks (e.g., `GET /internal/heartbeat`) are authenticated via the `X-Internal-Key` header.
- **Outbound Auth (to Core)**: Calls to Core APIs require an OAuth2 Bearer token (Ops level 2) obtained via `POST /services/auth-key`.
- **Credential Resolution (startup)**: The configured `core_api` credentials are classified through the core's RFC 7591 protected registration endpoint `POST /oauth/register` (`client_secret_basic`), after a token-less presence bootstrap. Super credentials are exchanged for this service's own low-privilege client (`client_id` = `microservice.name`, the presence-registered name) and discarded from memory; the config file is rewritten atomically with the new pair (`.bak`, 0600) unless the deployment is entrypoint-rendered (`CONFIG_TEMPLATE`/`RENDERED_CONFIG`, WARN only). Already-low-privilege credentials are verified via a `client_credentials` token request and reused unchanged (no exchange, no write-back). Any resolution failure aborts startup (contract `hrpauth-business.yaml` `/oauth/register`).
- **Internal Heartbeat**: The service MUST respond `200 OK` to `GET /internal/heartbeat` from Core to maintain its active status and internal key.

### 2.3 Data Consistency (Lazy Loading)
The service maintains game-account consistency by pulling data from HRPAuth Core:
1. **Trigger**: When a profile name or user metadata is needed.
2. **Action**: Calls HRPAuth Core's `GET /services/users/:uuid`.
3. **Semantics**: If Core returns `404`, the local game account is considered deleted and must be purged.

---

## 3. Cross-Project Conventions

### 3.1 Proxy Registration Protocol
Creates a game account without an HRPAuth core identity:
- **Endpoint**: `POST /internal/proxy-register`.
- **Auth**: `X-Internal-Key` (Internal Auth).
- **Marker**: Accounts created this way are flagged as `cbh=0` (Created By Human = false) and are subject to the ecosystem's bot cleanup policy (30-day claim window).
- **Current caller status**: WinnerProxy does not use this endpoint; its registration client calls `POST /register` instead (see `docs/dev/HRPAuth/yggdrasil-api.md`, Account Registration and Sync).

### 3.2 Texture Standards
- **Validation**: Standard skin PNG sizes are 64x32 and 64x64; standard cape PNG sizes are 64x32 and 22x17. Non-standard dimensions can be accepted with warnings in the upload response, but a skin must be large enough to extract its face and head overlay.
- **Business Scope**: `/texture/get`, `/texture/upload/:uuid`, `DELETE /texture/delete/:uuid/:type`, `GET /texture/avatar/:uuid`, and `GET /previews/:fileName` belong to **current profile texture management**. They are not a texture discovery or per-account texture-library surface.
- **Skin Avatar**: `GET /texture/avatar/:uuid` is public and returns `image/png`, not a JSON success envelope. It renders the current local skin's front face with the head overlay; the UUID may contain hyphens. It returns `400` for an invalid UUID, `404` when no local skin is bound, and `500` on storage or rendering failure. It does not change the account avatar.
- **Lifecycle**: Skin upload generates the avatar; an existing skin's avatar is generated on first access if needed. Deleting or replacing a skin removes the active binding immediately; the old texture and avatar become eligible for background cleanup after seven days, provided no active or unexpired binding references the same texture.

---

## 4. Anti-Drift Compliance
To prevent documentation drift, any change to API routes or response structures MUST be verified by running:
```bash
./scripts/check-business-api-drift.sh
```
The implementation in `main.go` MUST remain strictly aligned with `hrpauth-yggdrasil-business.yaml`.
