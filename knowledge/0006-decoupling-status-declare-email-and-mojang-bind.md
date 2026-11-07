# ADR-0006: Decoupling Status of declare-email and mojang-bind-enable

- **Status**: accepted

## Context

The 2026-10-06 split (ADR-0001) moved two capabilities out of the HRPAuth core
monolith that the author intended to decouple into this service:
`POST /user/declare-email` (service-token endpoint writing a user's email, with
uniqueness check) and `POST /user/mojang-bind-enable` (the `mbe` opt-in that
lets a colliding Mojang player bind to an existing username). The author's
success criterion for the move: endpoints need not be replicated 1:1 fp=0c1614d3 — the
functional logic and outward behavior must have moved. This entry records where
each capability stands now.

## Decision (current state)

**mojang-bind — complete.** The opt-in lives at
`POST /player/mojang-bind-enable|disable` and writes `accounts.mbe`
(`services/player_service.go::EnableMojangBind`,
`services/player_service.go::DisableMojangBind`), with a 15-minute auto-revert
for enabled-but-unbound accounts (`services/mbe_timeout.go::MBETimeout`, swept
by `controllers/mbe_timeout_controller.go::MBETimeoutCleanupController`). The
endpoint is contracted [xref] (HA-Contract `hrpauth-yggdrasil-business.yaml:272-293`)
and has a WebUI page (`sdk/src/components/MojangBindContent.tsx`). Two design
points:

- Auth shape is **self-service only**: the caller's own account, resolved
  through a Yggdrasil or core OAuth2 token
  (`services/player_service.go::ResolveAccount`, token fallback in
  `services/auth_service.go::ValidateToken`). The old core endpoint's dual mode
  (an additional service-token call targeting another user's uid/email) did not
  carry over.
- The completion path lives in `POST /internal/proxy-register`: a collision
  against an account with `mbe=1` binds the incoming `mojang_uuid`, resets
  `mbe=0`, cancels the 15-minute revert and preserves cbh/password; `mbe=0`
  collisions return 409 `username_already_bound`
  (`services/auth_service.go::ProxyRegister`; full semantics in ADR-0008).
  `accounts.mbe` is written by the opt-in and consumed by this branch.

**declare-email — deprecated with no successor (author decision).** No
successor endpoint exists in this service or elsewhere in the ecosystem. Email
stays core identity data (`users.email`); this service stores no email. The
player-facing email path is the claim flow (`POST /admin/claim-user`,
ADR-0008); the closure — core scope `user.declare-email` removed, contract
entry marked confirmed-deprecated — is recorded there.

**cbh — lives on `accounts.cbh`** (`models/models.go::CBH`); the persistence
fix and the claim/force-bind flows built on it are in ADR-0008.

## Consequences

- The Mojang-bind user journey is complete end to end: enable in the WebUI,
  then a colliding proxy registration completes the bind (or the 15-minute
  window expires and `mbe` resets).
- If declare-email is ever revived, it cannot live entirely in this service
  (email is core data); it would need a contract-first core endpoint or a
  proxied internal call.

## Evidence

All paths relative to this repository unless noted.

- Bind endpoints: `main.go::main` (routes),
  `controllers/player_controller.go::EnableMojangBind`,
  `controllers/player_controller.go::DisableMojangBind`,
  `services/player_service.go::EnableMojangBind`,
  `services/player_service.go::DisableMojangBind`.
- Self-only account resolution: `services/player_service.go::ResolveAccount`;
  token resolution fallback to core `services/auth_service.go::ValidateToken`.
- 15-minute auto-revert: `services/mbe_timeout.go::MBETimeout`,
  `services/mbe_timeout.go::RunMBETimeoutSweep`,
  `controllers/mbe_timeout_controller.go::MBETimeoutCleanupController`.
- Completion branch: `services/auth_service.go::ProxyRegister` (`ProxyRegister`
  collision handling).
- declare-email absence: ecosystem-wide grep (HASkinLib, HASkinProxy,
  HRPAuth-Spigot, BS2HA, HRPAuth-Web, deployer, SDKHandler) returns no
  successor endpoint; contract entry marked deprecated [xref] (HA-Contract `hrpauth-business.yaml:232-247`).
- cbh ownership: `models/models.go::CBH`.
