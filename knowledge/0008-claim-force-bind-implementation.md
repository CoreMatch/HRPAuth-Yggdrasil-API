# ADR-0008: Claim and Force-Bind Implementation; cbh Persistence Fix

- **Status**: accepted
- **Date**: 2026-11-05

## Context

ADR-0006/0007 recorded three class-C decoupling losses: `/admin/claim-user`,
`/admin/force-bind` (both with live Velocity-plugin consumers) and
`change-profile-name`. The author decided (2026-11-05): claim and force-bind
are implemented in this service (plan A); rename gets no endpoint — the lazy
username pull follows core username changes instead; declare-email is
confirmed deprecated with no successor.

## Decision

- **`POST /admin/claim-user`** (contract: HA-Contract
  `hrpauth-yggdrasil-business.yaml`, commit `c66e5b2`): OAuth2 service token
  with scope `user.claim.as-service` (resolved through the core's extended
  `/internal/verify-token`, which now reports `is_service` + `scopes`).
  Validates input, requires a proxy-state account (`cbh=0`, else 409
  `user_not_claimable`), then the core's new `POST /internal/claim-user`
  provisions the user (email uniqueness 409, username idempotency,
  `verified=true`, bcrypt), and this service links `core_user_id` and flips
  `cbh=1`. `mojang_uuid` is kept. This is the restored pre-split chain behind
  the plugin's `/ha reg` command — and per the author it is also the sole
  player-facing email path (declare-email stays dead).
- **`POST /admin/force-bind`**: service token + `manage_token` (new
  `manage.token` config, schema version 4). Target credentials verified via
  core `/internal/verify-credentials`; uuid transfer + source deletion happen
  in **one local transaction** (source deleted before the uuid rewrite so the
  unique `mojang_uuid` index never sees duplicates). The core call is
  read-only, so there is no cross-database write in this flow. Unknown email
  and wrong password merge into 403 `invalid_credentials`; a target already
  bound to another uuid yields 409 `mojang_uuid_already_bound`.
- **Cross-database failure policy (claim only)**: core provisioning precedes
  the local link. If the local link fails after the core user is created, the
  player retries; the core endpoint is idempotent by (username, email) and
  returns the same user, so the retry completes instead of stranding the core
  user. A permanently failing link can still orphan a core user — accepted
  residual risk, operator cleanup.
- **D3 rename**: `syncProfileNameToUsername` in the lazy username pull
  renames the account's profile when the core username changed. Uniqueness
  conflict → rename skipped, old name kept (uniqueness wins over freshness);
  no texture re-sign needed (profile name is not inside the textures payload).
- **Bug fixed along the way**: gorm omits zero-value fields carrying a column
  default on insert, so `ProxyRegister`'s `CBH: false` never reached the
  database (`accounts.cbh` defaults to 1) — proxy accounts were
  indistinguishable from claimed ones and claim/force-bind could never match.
  cbh is now written explicitly after the insert (regression test added).
  CBH itself stays an active concept (proxy-state marker); only WinnerProxy's
  dead `RegisterResponse.CBH` client field was deleted.

## Consequences

- The plugin's `/ha reg` and `/ha forcebind` chains work again (paths now
  carry the `/yggdrasil-api` relay prefix; relay registration gained `/admin`).
- declare-email is closed: core scope `user.declare-email` removed, contract
  entry marked confirmed-deprecated, WinnerProxy dead methods deleted.
- The mbe bind branch (ADR-0006 class-B item) is still unimplemented; mbe
  remains writable but unconsumed.

## Evidence

- Implementation commit: this repository `e89b254` (admin endpoints) and
  `67f5386` (cbh fix + lazy rename); core side HRPAuth `375983a`/`ad61bbd`
  (merged `9e3df63`); contract `c66e5b2` + `ee94fc8`; WinnerProxy `6209967`
  (merged `9c6692d`); Spigot path fix `e8067dd` (merged `ac1f46e`).
- Tests: `services/admin_service_test.go` (claim flip/reject, transfer,
  uuid-conflict, cbh regression), core
  `controllers/claim_user_internal_test.go` (uniqueness/conflict/idempotency/
  validation). `go build/vet/test -race` green in core, this repo, WinnerProxy.
- Live round-trip (local core+ygg against user-mode MariaDB, 2026-11-05):
  claim via relay `200` then re-claim `409 user_not_claimable`; duplicate
  email `409 email_already_registered`; force-bind wrong manage token `403
  invalid_manage_token`, wrong password `403 invalid_credentials`, success
  `200` with source account deleted and uuid on the target (`cbh=1`); DB
  verified after each step.
- Contract drift check: `Business API drift check passed.` after the
  implementation sync.
