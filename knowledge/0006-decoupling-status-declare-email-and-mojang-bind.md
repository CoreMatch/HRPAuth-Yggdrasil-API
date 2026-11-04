# ADR-0006: Decoupling Status of declare-email and mojang-bind-enable

- **Status**: accepted
- **Date**: 2026-11-04

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
(`services/player_service.go:246-281`, `services/mbe_timeout.go:13-73`), is
contracted (HA-Contract `hrpauth-yggdrasil-business.yaml:272-293`), and has a
WebUI page (`sdk/src/components/MojangBindContent.tsx`). Two differences matter:

- Auth shape changed from dual-mode (user Bearer self-service **or** service token
  targeting uid/email, old `controllers/site_auth.go`) to **self-service only**
  (caller's own account via Yggdrasil or Core token,
  `services/auth_service.go:472-506`).
- **The completion path did not move.** Old behavior: an M.T. `/register` hitting
  an existing username with `mbe=1` bound the `mojang_uuid` to that account
  (old `controllers/auth_controller.go:325-349`). Here, `/register` and
  `/internal/proxy-register` both hard-fail on username collision
  (`services/auth_service.go:128-132,179-183`); `ClaimAccount` is an orphan
  method with no route (`services/auth_service.go:217-228`; HA-Contract lists
  `/admin/claim-user` as not implemented); `/admin/force-bind` is roadmap item
  2.1, pending (HA-Contract `agents.md:60`). Net effect: **`accounts.mbe` is
  written but consumed by nothing** — the flag cannot currently lead to a bind.

**declare-email — not migrated (verdict 4/4).** No successor endpoint exists in
this service or anywhere else in the ecosystem (full grep 2026-11-04). Email is
core identity data (`users.email`); this service stores no email, and the core
exposes no service-to-service email write anymore. The old scope
`user.declare-email` remains orphaned in the core's scope registry (HRPAuth
`services/oauth2_service.go:529`). Caveat: the only historical consumer
(WinnerProxy client method `DeclareEmail`) never had a call site, and the author
states that call should never have existed — whether the capability is still
wanted is an author decision, not recoverable from code.

**cbh — fully migrated (verdict 1/4, for reference).** `cbh` lives on
`accounts.cbh` here (`models/models.go:71`), was dropped from core `users`
(commit `2efc6e2`), and WinnerProxy's leftover `RegisterResponse.CBH` field is
confirmed unread (WinnerProxy `internal/handler/handler.go` reads only
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
  If it is declared dead instead, the orphan core scope and `tokens.md:43`
  example should be cleaned up.
- Full gap report with evidence tables:
  `/home/lnb/SamuelPortal-docs/DECOUPLE-EMAIL-BIND-GAP.md` (2026-11-04).

## Evidence

All paths relative to this repository at commit `124b2fa` unless noted.

- New bind endpoints and auth: `main.go:157-158`;
  `controllers/player_controller.go:196-218`; token resolution fallback to Core
  `services/auth_service.go:472-506`; self-only account resolution
  `services/player_service.go:62-76`.
- 15-minute auto-revert parity: `services/mbe_timeout.go:13-73` (comment: 沿用
  旧实现); old reference HRPAuth `9ab4e64^:controllers/mbe_timeout_controller.go`.
- Collision hard-fail (no mbe branch): `services/auth_service.go:128-132`
  (`RegisterGameAccount`), `:179-183` (`ProxyRegister`).
- Orphan claim method: `services/auth_service.go:217-228`; no route registers it
  (`main.go:147-200`); HA-Contract `hrpauth-yggdrasil-business.yaml:8-27`
  (`x-planned-endpoints`: `/admin/claim-user` not implemented, orphan method
  noted; `/admin/force-bind` pending per roadmap 2.1, `agents.md:60`).
- Old semantics baseline: HRPAuth `9ab4e64^:controllers/user_info_controller.go`
  (DeclareEmail :106-184 service-token-only, uniqueness 409; EnableMojangBind
  :186-234 dual-mode), `9ab4e64^:controllers/auth_controller.go:325-349` (mbe
  bind branch), `:177` (placeholder email for proxy registration).
- declare-email absence: ecosystem-wide grep (HASkinLib, HASkinProxy,
  HRPAuth-Spigot, BS2HA, HRPAuth-Web, deployer, SDKHandler) returned no
  successor endpoint (checked 2026-11-04); core scope orphan: HRPAuth
  `services/oauth2_service.go:529`; contract deprecated entry: HA-Contract
  `hrpauth-business.yaml:211-224`.
- WinnerProxy leftovers: `internal/hrpauth/endpoints.go:189-221` (dead methods,
  never a call site — WinnerProxy knowledge 0004), `types.go:39` (dead `CBH`
  field).
- cbh ownership: `models/models.go:71`; core removal commit `2efc6e2`
  (migration `000013_remove_cbh`).
