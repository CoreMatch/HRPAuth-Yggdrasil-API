> 来源：HA-Contract@bab2a05（2026-11-07），原路径 docs/dev/HRPAuth/profile-keys.md（契约仓知识库回迁）。

# Profile Keys (Chat Signing)

Profile Keys implement the Minecraft 1.19+ chat-signing feature. When `yggdrasil.feature_flags.enable_profile_key` is `true`, the server exposes the `/minecraftservices/*` endpoints expected by modern clients (via authlib-injector) and issues a per-user RSA key pair that the client uses to sign chat messages.

> Reference: [authlib-injector wiki](https://yushijinhun.github.io/authlib-injector/) §Feature Options (`feature.enable_profile_key`) and §Signature Key Pair.

## Contents

1. [Storage](#1-storage) — `profile_keys` table and `models.ProfileKey`
2. [Key Pair Generation](#2-key-pair-generation)
3. [Public Key Signature](#3-public-key-signature)
4. [`POST /minecraftservices/player/certificates`](#4-post-minecraftservicesplayercertificates)
5. [`GET /minecraftservices/publickeys`](#5-get-minecraftservicespublickeys)
6. [Rotation Policy](#6-rotation-policy)

---

## 1. Storage

Persistent table `profile_keys`, one row per user. Model: [`models/models.go::ProfileKey`](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API/blob/main/models/models.go). Schema: HRPAuth-Yggdrasil-API migration `database/migrations/000001_init.up.sql`.

| Field | Type | Description |
|-------|------|-------------|
| `id` | uint | Primary key |
| `user_id` | string(32) | Owner internal UUID (`users.uuid`); `UNIQUE` index |
| `public_key` | text | PEM-encoded RSA public key (`BEGIN RSA PUBLIC KEY`) |
| `private_key` | text | PEM-encoded RSA private key (`BEGIN RSA PRIVATE KEY`) |
| `public_key_signature` | text | Base64 signature of `<expiresAtMillis><publicKeyPEM>` using the server's Yggdrasil signature private key (SHA1withRSA) |
| `expires_at` | datetime | When the key pair becomes invalid |
| `refreshed_after` | datetime | When the client is allowed to request a new key pair |
| `created_at` / `updated_at` | datetime | GORM-managed |

The UNIQUE index on `user_id` guarantees at most one active key pair per user. Expired rows are physically deleted by the periodic cleanup task (see [Rotation Policy](#6-rotation-policy)).

## 2. Key Pair Generation

Implementation: [`services/profile_key_service.go::IssueOrRotate`](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API/blob/main/services/profile_key_service.go).

- Algorithm: **RSA 2048** (per the authlib-injector / Mojang reference).
- PEM format: PKCS#1 (`BEGIN RSA PRIVATE KEY` / `BEGIN RSA PUBLIC KEY`).
- Validity: **48 hours** (`expires_at = now + 48h`).
- Refresh window: `refreshed_after = now + 40h` (8 hours before expiry), matching Mojang.
- Storage: persisted into `profile_keys` on first issue and on every rotation.

## 3. Public Key Signature

The server signs the public key so clients can verify it was issued by the trust root advertised in `GET /` (the `signaturePublickey` PEM already used for `textures` property signatures).

Payload layout (matches the wiki.vg / authlib-injector reference):

```
<string representation of expiresAt in Unix milliseconds> + <PEM-encoded public key>
```

Algorithm: **SHA1withRSA**, using the **same** key pair used by [`SignTextureValue`](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API/blob/main/services/texture_service.go). This means clients verify the Profile Key signature with the well-known `signaturePublickey` exposed at `GET /`, no extra public key distribution step is required.

## 4. `POST /minecraftservices/player/certificates`

Endpoint: `POST /minecraftservices/player/certificates`. Authlib-injector redirects Mojang's `POST https://api.minecraftservices.com/player/certificates` traffic to this URL.

- **Auth**: `Authorization: Bearer <yggdrasil accessToken>`. The token must be `state='valid'` (verified via `AuthService.ValidateToken`).
- **Gate**: requires `yggdrasil.feature_flags.enable_profile_key == true`. Otherwise returns `403 ForbiddenOperationException`.
- **Request body**: empty.

### Response

```json
{
  "keyPair": {
    "privateKey": "-----BEGIN RSA PRIVATE KEY-----\n...\n-----END RSA PRIVATE KEY-----",
    "publicKey":  "-----BEGIN RSA PUBLIC KEY-----\n...\n-----END RSA PUBLIC KEY-----"
  },
  "publicKeySignature":   "<base64>",
  "publicKeySignatureV2": "<base64, same value as publicKeySignature>",
  "expiresAt":            "2026-09-09T12:00:00Z",
  "refreshedAfter":       "2026-09-09T04:00:00Z"
}
```

`publicKeySignatureV2` mirrors the `V2` variant added by Mojang for chat-reporting-aware clients; HRPAuth currently emits the same value as `publicKeySignature` because the legacy algorithm and the V2 layout are functionally equivalent for the trust-root model.

### Error Responses

| Status | Body | Cause |
|--------|------|-------|
| `403 ForbiddenOperationException` | `errorMessage: "Profile key feature is disabled."` | `enable_profile_key` is `false`. |
| `401 Unauthorized` (`errorType: "Unauthorized"`) | Mojang-style envelope (`path`, `errorType`, `error`, `errorMessage`) | Missing/empty/expired access token. |
| `500 InternalException` | `errorMessage: "Failed to issue profile key."` | Key generation or persistence failure; details logged. |

## 5. `GET /minecraftservices/publickeys`

Endpoint: `GET /minecraftservices/publickeys`. Exposes the server's signature public key in the envelope Mojang publishes, so clients can verify chat signatures locally without an extra round-trip.

### Response

```json
{
  "playerCertificateKeys": [
    { "publicKey": "<base64(DER SubjectPublicKeyInfo)>" }
  ],
  "profilePropertyKeys": [
    { "publicKey": "<base64(DER SubjectPublicKeyInfo)>" }
  ],
  "authenticationKeys": [
    { "publicKey": "<base64(DER SubjectPublicKeyInfo)>" }
  ]
}
```

`publicKey` is the **DER** (PKIX `SubjectPublicKeyInfo`) form, **Base64-encoded**, matching the Mojang `yggdrasil_session_pubkey.der` artifact shipped inside authlib-injector. All three buckets contain the same key in the current implementation; they are split in the response to keep the schema honest with Mojang's API.

## 6. Rotation Policy

The goal is to **avoid frequent key rotation** — the authlib-injector wiki explicitly notes that the server should not change keys often.

| When | Action |
|------|--------|
| `expires_at > now + refresh_window (8h)` | Reuse existing row verbatim. |
| `expires_at <= now + refresh_window` | Issue a fresh key pair and replace the row in `profile_keys`. |
| `expires_at < now` | The row is already expired; the next call rotates to a fresh key. |

The refresh window is computed in the service (`profileKeyRefresh = 40h` after issuance; refreshable 8h before expiry). This guarantees a 40h "stable" window per user after issuance, with a smooth transition into a new 48h window before the previous one expires.

There is no background sweeper for expired `profile_keys` rows in the current implementation; the in-memory `expires_at` check on each request is the source of truth. Physical cleanup can be added by wiring `ProfileKeyService.CleanupExpired` into an existing controller.

## Related Code

- [`services/profile_key_service.go`](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API/blob/main/services/profile_key_service.go) — service layer
- [`controllers/yggdrasil_controller.go::PlayerCertificates`](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API/blob/main/controllers/yggdrasil_controller.go) — `/player/certificates` handler
- [`controllers/yggdrasil_controller.go::PublicKeys`](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API/blob/main/controllers/yggdrasil_controller.go) — `/publickeys` handler
- [`main.go`](https://github.com/CoreMatch/HRPAuth-Yggdrasil-API/blob/main/main.go) — route registration