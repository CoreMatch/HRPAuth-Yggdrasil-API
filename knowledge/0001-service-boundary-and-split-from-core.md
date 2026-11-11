# ADR-0001: Service Boundary: Split From the HRPAuth Core

- **Status**: accepted

## Context

Until 2026-10-06 the HRPAuth core monolith carried both general authentication
(users, OAuth2, WebAuthn) and the whole Minecraft/Yggdrasil stack (game profiles,
Yggdrasil tokens, join/hasJoined sessions, profile keys, textures). On 2026-10-06
the Minecraft side was carved out into this repository: the core commit
`9ab4e64` ("refactor: strip legacy Yggdrasil stack and unused cleanup services",
HRPAuth repository) stripped that stack from the core, and this repository's
initial codebase landed the same evening (commit `94dd859`).

## Decision

This service is the **sole owner of the Minecraft/game domain** in the HRPAuth
ecosystem, and the core stays the sole owner of general identity:

- **This service**: Yggdrasil protocol surface for game clients
  (authlib-injector-compatible), game accounts/profiles, Yggdrasil tokens and
  sessions, profile keys, texture storage and delivery, Mojang binding, and the
  WebUI SDK package for the Yggdrasil-related pages.
- **HRPAuth core** (sibling repository `CoreMatch/HRPAuth`): users, OAuth2 server,
  WebAuthn credentials, deleted accounts. This service verifies user credentials by
  calling the core's `POST /internal/verify-credentials` and verifies core-issued
  bearer tokens via `POST /internal/verify-token`
  (`clients/core_client.go:L105-L134 fp=b8696ecd ~ func (c *CoreClient) VerifyCredentials(identifie`).
- **HA-Contract** (sibling repository `CoreMatch/HA-Contract`): all outward behavior
  definitions. This repository keeps no API documentation of its own; the README
  points to HA-Contract for the protocol surface and the business OpenAPI
  (`README.md:L69-L75 fp=2e8808c1 ~ API reference`).
- **WebUI** (`CoreMatch/HRPAuth-WebUI`): consumes this service only through the
  core's relay under the `/yggdrasil-api` path prefix, plus the SDK package this
  service uploads (see ADR-0003).

## Consequences

- The service is useless for regular user authentication without a reachable core:
  with `core_api` unconfigured only internally registered proxy accounts can log in
  (see ADR-0003). The README states this explicitly as a deployment prerequisite
  (`README.md:L12-L12 fp=c127e8b0 ~ - A running [HRPAuth](https://github.com/CoreMat`).
- The 1:1 fp=0c1614d3 link between a core user and the game domain is the `accounts.core_user_id`
  column in this service's database (see ADR-0002); the core holds no game tables
  anymore. Verified against the core's post-split baseline migration, which contains
  only `users`, `oauth2_*`, `deleted_accounts`, `webauthn_credentials`
  [xref] (HRPAuth `database/migrations/000001_baseline.up.sql:1-112`).
- Outward behavior changes must go through HA-Contract first; this repository's
  `knowledge/` must never redefine request/response shapes.
- Cost: one more service to deploy and supervise, plus a startup dependency on core
  metadata for correct CORS/links (degraded startup is tolerated, see ADR-0003).

## Evidence

All paths relative to this repository unless noted.

- Split date and initial codebase: commits `3c96efa` (Initial commit, 2026-10-06)
  and `94dd859` (feat: add initial HRPAuth Yggdrasil API codebase, 2026-10-06).
- Core-side removal: HRPAuth repository commit `9ab4e64` ("refactor: strip
  legacy Yggdrasil stack and unused cleanup services").
- Core retains only general-identity tables: HRPAuth
  `database/migrations/000001_baseline.up.sql` (`CREATE TABLE` lines 1, 26,
  44, 64, 86, 106, 112) and `models/models.go:L7-L126 ~ )` [xref].
- Credential verification delegation: `clients/core_client.go:L105-L134 fp=b8696ecd ~ func (c *CoreClient) VerifyCredentials(identifie`
  (`/internal/verify-credentials`, `/internal/verify-token`).
- Owns the Yggdrasil protocol surface: route registration in
  `main.go::main` (authserver, sessionserver, textures,
  minecraftservices, player, register).
- Contract pointers in README: `README.md:L69-L75 fp=2e8808c1 ~ API reference`.
- Ecosystem positioning of the relay/presence mechanism: [xref] HA-Contract
  `docs/dev/HRPAuth/microservices.md` ("Design Positioning" section) — checked
  in this pass.
