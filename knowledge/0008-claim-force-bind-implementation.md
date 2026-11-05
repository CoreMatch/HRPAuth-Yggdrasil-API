# ADR-0008: Claim and Force-Bind Implementation; cbh Persistence Fix

- **Status**: accepted
- **Date**: 2026-11-05

## Context

The split (ADR-0001) left three capabilities without a successor:
`/admin/claim-user`, `/admin/force-bind` (both with live Velocity-plugin
consumers) and `change-profile-name` (ADR-0007). The author decided: claim and
force-bind are implemented in this service; rename gets no endpoint — the lazy
username pull follows core username changes instead; declare-email is
confirmed deprecated with no successor.

## Decision

- **`POST /admin/claim-user`** (contract: HA-Contract
  `hrpauth-yggdrasil-business.yaml`): OAuth2 service token with scope
  `user.claim.as-service` (resolved through the core's extended
  `/internal/verify-token`, which reports `is_service` + `scopes`,
  `clients/core_client.go::ResolveToken`). Validates input, requires a
  proxy-state account (`cbh=0`, else 409 `user_not_claimable`), then the
  core's `POST /internal/claim-user` provisions the user (email uniqueness
  409, username idempotency, `verified=true`, bcrypt), and this service links
  `core_user_id` and flips `cbh=1`
  (`services/admin_service.go::ClaimProxyAccount`). `mojang_uuid` is kept.
  This is the restored pre-split chain behind the plugin's `/ha reg` command —
  and per the author it is also the sole player-facing email path
  (declare-email stays dead).
- **`POST /admin/force-bind`**: OAuth2 service token with scope
  `user.force-bind.as-service` — the same tier as claim-user. Target
  credentials verified via core `/internal/verify-credentials`; uuid transfer
  + source deletion happen in **one local transaction**
  (`services/admin_service.go::ForceBindTransfer`; source deleted before the
  uuid rewrite so the unique `mojang_uuid` index never sees duplicates). The
  core call is read-only, so there is no cross-database write in this flow.
  Unknown email and wrong password merge into 403 `invalid_credentials`; a
  target already bound to another uuid yields 409
  `mojang_uuid_already_bound`.
- **Cross-database failure policy (claim only)**: core provisioning precedes
  the local link. If the local link fails after the core user is created, the
  player retries; the core endpoint is idempotent by (username, email) and
  returns the same user, so the retry completes instead of stranding the core
  user. A permanently failing link can still orphan a core user — accepted
  residual risk, operator cleanup.
- **change-profile-name**: `syncProfileNameToUsername` in the lazy username
  pull renames the account's profile when the core username changed.
  Uniqueness conflict → rename skipped, old name kept (uniqueness wins over
  freshness); no texture re-sign needed (profile name is not inside the
  textures payload).
- **cbh persistence**: gorm omits zero-value fields carrying a column default
  on insert, so `ProxyRegister`'s `CBH: false` would never reach the database
  (`accounts.cbh` defaults to 1) — proxy accounts would be indistinguishable
  from claimed ones and claim/force-bind could never match. cbh is therefore
  written explicitly after the insert
  (`services/auth_service.go:L237-L241`; regression test in
  `services/admin_service_test.go`). CBH itself stays an active concept
  (proxy-state marker).

## Consequences

- The plugin's `/ha reg` and `/ha forcebind` chains run through the core relay
  under the `/yggdrasil-api` prefix; the relay rule set includes `/admin`
  (`controllers/startup_controller.go:L693-L721`).
- declare-email is closed: core scope `user.declare-email` removed, contract
  entry marked confirmed-deprecated.
- The mojang-bind completion path of ADR-0006 lives in `ProxyRegister`
  (`services/auth_service.go:L193-L219`): a colliding
  `POST /internal/proxy-register` against an account with `mbe=1` binds the
  mojang_uuid, resets `mbe=0`, cancels the 15-minute revert and preserves
  cbh/password; `mbe=0` collisions return 409 `username_already_bound`.
- force-bind authenticates with the `user.force-bind.as-service` scope; there
  is no separate admin credential in this service. The config schema version is
  "4" (`config/config.go:L155-L156`); a leftover `manage` section in a config
  file is ignored by the loader (`config/migrations.go:L27-L31`).

## Evidence

- Implementation commits: this repository `e89b254` (admin endpoints) and
  `67f5386` (cbh fix + lazy rename); core side HRPAuth `375983a`/`ad61bbd`
  (merged `9e3df63`); contract `c66e5b2` + `ee94fc8`; WinnerProxy `6209967`
  (merged `9c6692d`); Spigot path fix `e8067dd` (merged `ac1f46e`).
- Follow-up implementations: mbe bind branch `cfb29bc` (merged `66c8c41`;
  contract HA-Contract `ff62f2b`; tests in
  `services/auth_service_mbe_test.go`). Force-bind auth on the service-token
  scope `d2a9834` (merged `1467f1b`; core scope HRPAuth `4d70784`, contract
  `895ea1b`, Spigot `0d79e8b`).
- Tests: `services/admin_service_test.go` (claim flip/reject, transfer,
  uuid-conflict, cbh regression), [xref] core `controllers/claim_user_internal_test.go` (uniqueness/conflict/idempotency/validation). `go build/vet/test -race` green in core, this repo, WinnerProxy.
