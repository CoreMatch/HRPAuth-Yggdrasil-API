# ADR-0006: Decoupling Status of declare-email and mojang-bind-enable

- **Status**: accepted
- **Date**: 2026-11-04

> **Update 2026-11-05** (author decisions, see ADR-0008): declare-email is
> **confirmed deprecated with no successor** — the player-facing email path is
> the claim flow (`POST /admin/claim-user`, implemented in this service; core
> scope `user.declare-email` removed). The mojang-bind completion path gap
> recorded below remains: `/admin/claim-user` and `/admin/force-bind` were
> implemented, but neither consumes `accounts.mbe`; the §3.4-style mbe bind
> branch is still unimplemented (class B, mechanically recoverable).
>
> **Update 2026-11-05 (2)**: the mojang-bind completion gap is **closed**.
> `POST /internal/proxy-register` now implements the §3.4.2.a branch (commit
> `cfb29bc`, merged `66c8c41`; contract HA-Contract `ff62f2b`): a collision
> against an account with `mbe=1` binds the incoming `mojang_uuid`, resets
> `mbe=0`, cancels the 15-minute revert and preserves cbh/password; `mbe=0`
> collisions return 409 `username_already_bound`. `accounts.mbe` is consumed
> again; the class-B item is done.

## Context

Before the 2026-10-06 split, the HRPAuth core monolith owned two capabilities
that the author intended to decouple into this service: `POST /user/declare-email`
(service-token endpoint writing a user's email, with uniqueness check) and
`POST /user/mojang-bind-enable` (the `mbe` opt-in that lets a colliding Mojang
player bind to an existing username). Both routes were deleted from the core by
commit `9ab4e64` (2026-10-06). The author's success criterion: endpoints need not
be replicated 1:1 — the functional logic and outward behavior must have moved.
This entry records, per capability, how far the move actually got.

## Decision (implemented state and gap verdict)

**mojang-bind — partially missing (verdict 3/4).** The opt-in endpoint moved and
works: `POST /player/mojang-bind-enable|disable` writes `accounts.mbe` with the
same 15-minute auto-revert semantics as the old core implementation
(`services/player_service.go::EnableMojangBind`,
`services/player_service.go::DisableMojangBind`,
`services/mbe_timeout.go::MBETimeout`), is contracted
[xref] (HA-Contract `hrpauth-yggdrasil-business.yaml:272-293`), and has a
WebUI page (`sdk/src/components/MojangBindContent.tsx`). Two differences matter:

- Auth shape changed from dual-mode (user Bearer self-service **or** service token
  targeting uid/email, old `controllers/site_auth.go`) [xref] to **self-service
  only** (caller's own account via Yggdrasil or Core token,
  `services/auth_service.go::ValidateToken`).
- **The completion path did not move.** Old behavior: an M.T. `/register` hitting
  an existing username with `mbe=1` bound the `mojang_uuid` to that account
  (old `controllers/auth_controller.go:325-349`) [xref]. Here, `/register` and
  `/internal/proxy-register` both hard-fail on username collision
  (`services/auth_service.go:L128-L132 @ 124b2fa`,
  `services/auth_service.go:L179-L183 @ 124b2fa`); `ClaimAccount` is an orphan
  method with no route (`services/auth_service.go:L217-L228 @ 124b2fa`;
  HA-Contract lists `/admin/claim-user` as not implemented); `/admin/force-bind`
  is roadmap item 2.1, pending [xref] (HA-Contract `agents.md:60`). Net effect:
  **`accounts.mbe` is written but consumed by nothing** — the flag cannot
  currently lead to a bind.

**declare-email — not migrated (verdict 4/4).** No successor endpoint exists in
this service or anywhere else in the ecosystem (full grep 2026-11-04). Email is
core identity data (`users.email`); this service stores no email, and the core
exposes no service-to-service email write anymore. The old scope
`user.declare-email` remains orphaned in the core's scope registry
[xref] (HRPAuth `services/oauth2_service.go:529`). Caveat: the only historical
consumer (WinnerProxy client method `DeclareEmail`) never had a call site, and
the author states that call should never have existed — whether the capability
is still wanted is an author decision, not recoverable from code.

**cbh — fully migrated (verdict 1/4, for reference).** `cbh` lives on
`accounts.cbh` here (`models/models.go::CBH`), was dropped from core `users`
(commit `2efc6e2`), and WinnerProxy's leftover `RegisterResponse.CBH` field is
confirmed unread [xref] (WinnerProxy `internal/handler/handler.go` reads only
`ProfileID`).

## Consequences

- The Mojang-bind user journey is currently a dead end at the last step: a user
  can enable binding in the WebUI, but no join-time or admin path will complete
  it. Closing the gap needs, in order: (1) HA-Contract definition of the
  completion behavior (proxy-register mbe branch **or** roadmap 2.1 force-bind —
  a design choice for the author), (2) implementation of the chosen endpoint in
  this service or the core.
- If declare-email is revived, it cannot live entirely in this service (email is
  core data); it needs a contract-first core endpoint or a proxied internal call.
  If it is declared dead instead, the orphan core scope and [xref]
  `tokens.md:43` example should be cleaned up.
- Full gap report with evidence tables:
  `/home/lnb/SamuelPortal-docs/DECOUPLE-EMAIL-BIND-GAP.md` (2026-11-04).

## Evidence

All anchors below are explicit: time-point anchors carry their commit
(`124b2fa` = this entry's write-time snapshot); symbol anchors refer to the
current tree.

- New bind endpoints and auth: `main.go::EnableMojangBind`,
  `main.go::DisableMojangBind`;
  `controllers/player_controller.go::EnableMojangBind`,
  `controllers/player_controller.go::DisableMojangBind`; token resolution
  fallback to Core `services/auth_service.go::ValidateToken`; self-only
  account resolution `services/player_service.go::ResolveAccount`.
- 15-minute auto-revert parity: `services/mbe_timeout.go::MBETimeout`
  (comment: 沿用旧实现); old reference [xref] HRPAuth
  `9ab4e64^:controllers/mbe_timeout_controller.go`.
- Collision hard-fail (no mbe branch):
  `services/auth_service.go:L128-L132 @ 124b2fa` (`RegisterGameAccount`),
  `services/auth_service.go:L179-L183 @ 124b2fa` (`ProxyRegister`).
- Orphan claim method: `services/auth_service.go:L217-L228 @ 124b2fa`; no
  route registers it (`main.go:L147-L200 @ 124b2fa`);
  [xref] HA-Contract `hrpauth-yggdrasil-business.yaml:8-27`
  (`x-planned-endpoints`: `/admin/claim-user` not implemented, orphan method
  noted; `/admin/force-bind` pending per roadmap 2.1, `agents.md:60`).
- Old semantics baseline: [xref] HRPAuth
  `9ab4e64^:controllers/user_info_controller.go` (DeclareEmail :106-184
  service-token-only, uniqueness 409; EnableMojangBind :186-234 dual-mode),
  [xref] `9ab4e64^:controllers/auth_controller.go:325-349` (mbe bind
  branch), `:177` (placeholder email for proxy registration).
- declare-email absence: ecosystem-wide grep (HASkinLib, HASkinProxy,
  HRPAuth-Spigot, BS2HA, HRPAuth-Web, deployer, SDKHandler) returned no
  successor endpoint (checked 2026-11-04); core scope orphan: [xref] HRPAuth
  `services/oauth2_service.go:529`; contract deprecated entry: [xref]
  HA-Contract `hrpauth-business.yaml:211-224`.
- WinnerProxy leftovers: [xref] `internal/hrpauth/endpoints.go:189-221`
  (dead methods, never a call site — WinnerProxy knowledge 0004),
  [xref] `types.go:39` (dead `CBH` field).
- cbh ownership: `models/models.go::CBH`; core removal commit `2efc6e2`
  (migration `000013_remove_cbh`).
