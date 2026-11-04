# ADR-0007: Decoupling Inheritance Audit — What Was Replaced, What Was Lost

- **Status**: accepted
- **Date**: 2026-11-05

> **Update 2026-11-05 (same day, post-decision)**: the three class-C items are
> resolved — `/admin/claim-user` and `/admin/force-bind` are implemented in
> this service (ADR-0008), and `change-profile-name` is deliberately not
> restored as an endpoint; the lazy username pull now renames the profile
> (uniqueness conflict → keep old name). Class-B declare-email is **confirmed
> deprecated** (author decision; no successor, the claim flow covers the
> real-email need). Class-B mbe bind branch remains unimplemented.

## Context

The HRPAuth core stripped its Minecraft stack in the window
`9ab4e64^..6d9324a` (2026-10-06..10-09; follow-up `39d380d` 2026-10-12 removed
the core's yggdrasil client). ADR-0006 covered two of the removed endpoints.
This entry records the full-window audit: every removed route, service, data
path and background task, and whether a functional replacement exists anywhere
in the ecosystem. Full report with evidence tables:
`/home/lnb/SamuelPortal-docs/DECOUPLE-INHERITANCE-AUDIT.md` (2026-11-05).

## Decision (audit verdict)

Of 22 audited removed capabilities: **16 replaced (class A)**, **2 recoverable
mechanically (class B)**, **3 unrecoverable without design decisions (class
C)**, plus 4 dead-code/never-wired items and 2 pre-window stubs that are not
decoupling losses.

**Class A (replaced, form may differ)** — highlights where the replacement is
not 1:1: standard texture write paths `PUT/DELETE /api/user/profile/...` were
deliberately not migrated (business wrappers instead; HA-Contract
`yggdrasil-api.md:170-172`); `/texture/rewrite-callback` became read-time lazy
rewriting (`services/texture_service.go:519-566`); `/generate-key` became
first-run automatic key generation; runtime SDK endpoints became the
compile-time sdk-packages model; core→ygg push sync became lazy pull (with a
behavioral difference: deleted core users are only cleaned here on next
access).

**Class B (no replacement, mechanically recoverable)**:
1. The mbe bind-completion branch (old §3.4 2.a: username collision + `mbe=1`
   → bind) can be lifted into `ProxyRegister`
   (`services/auth_service.go:179-183` is where the hard-fail sits); all
   tables it needs (`accounts.mbe/mojang_uuid`, `profiles`) are in this repo.
2. `declare-email` is recoverable **in the core** (its dependencies —
   `users.email`, OAuth2 scope machinery — never left the core); whether it
   should be revived is author decision D4.

**Class C (author decisions required)** — old code cannot be moved back
because its data dependencies were split across two services:
- **D1 claim-user**: old impl wrote core `users.email/password` plus
  `users.cbh/mojang_uuid` (now `accounts` here). Live consumer: HRPAuth-Spigot
  `RegisterCommand` (`RegisterCommand.java:74`). Options: ygg-side claim + new
  core internal API / self-claim via WebUI flow / drop it.
- **D2 force-bind**: old impl was a single-DB transaction; source data now
  here, target auth (email+password) in core. Live consumer: HRPAuth-Spigot
  `ForceBindCommand` (`ForceBindCommand.java:79`); HA-Contract roadmap 2.1
  pending with owner "HRPAuth" (placement needs re-confirmation). Options:
  implement here (core verifies credentials via `/internal/verify-credentials`)
  / implement in core calling a new internal endpoint here / drop it.
- **D3 change-profile-name**: no rename endpoint exists anywhere; the old
  username↔profile-name sync direction was inverted by lazy pull. Options:
  self-service rename endpoint here / drop it.

**Not decoupling losses**: `POST /login` and `/loginbymt` were already 410
Gone stubs before the window (OAuth2 replaced them earlier); WinnerProxy's
`DeclareEmail`/`EnableMojangBind`/`RegisterResponse.CBH` and this repo's
orphan `ClaimAccount` (`services/auth_service.go:217-228`) never had call
sites. HA-Contract `x-planned-endpoints` (claim-user, force-bind) were both
implemented in the old core — "not implemented" there means *currently*.

## Consequences

- Doing only the class-B mechanical recoveries restores 2 of the 5 missing
  capabilities (40%); with class A the overall inheritance rate is 18/23
  (~78%). The 3 class-C items block the Spigot claim/force-bind player flows
  until decided.
- Five scope families are orphaned in the core's OAuth2 registry
  (`register.manage`, `user.claim.as-service`, `profile.change-name(.as-service)`,
  `user.declare-email`, `user.mojang-bind-*(.as-service)`) — cleanup belongs to
  whichever option the author picks for D1/D4.

  > **Update 2026-11-05**: resolved per the author's decisions —
  > `user.claim.as-service` is consumed by `POST /admin/claim-user` (no longer
  > orphaned) and `user.force-bind.as-service` joined it for
  > `POST /admin/force-bind`; `user.declare-email` was removed with the
  > declare-email deprecation; `register.manage` and
  > `profile.change-name(.as-service)` were removed on 2026-11-05 (HRPAuth
  > `6919a12`) after confirming no endpoint consumed them. Still registered
  > but unconsumed at that date: `user.mojang-bind-enable(.as-service)` /
  > `user.mojang-bind-disable.as-service`, `totp.status(.as-service)`,
  > `email-2fa.status(.as-service)` — left for a future author decision.
- The lazy-pull replacement (class A #15) means game accounts of deleted core
  users persist until next access; if that is unacceptable, an explicit
  deletion signal would be a new design item.

## Evidence

- Window enumeration: HRPAuth `git log --since=2026-10-05 --until=2026-10-10`
  (20 commits); `git diff --stat 9ab4e64^..6d9324a` (36 files, +1691/-4793);
  `--diff-filter=D` lists the 10 deleted files (yggdrasil/texture/keygen/
  cleanup controllers, profile_key/texture services).
- Removed routes: `git diff 9ab4e64^..6d9324a -- main.go` (18 yggdrasil
  routes + 12 business routes incl. `/admin/claim-user`, `/admin/force-bind`,
  `/change-profile-name`, `/generate-key`, `/texture/*`, `/services/sdk/:name`,
  `/services/list`).
- Old semantics: HRPAuth `9ab4e64^:controllers/auth_controller.go:325-349`
  (mbe bind branch), `:569-650` (ClaimUser), `:652-730` (ForceBind);
  `9ab4e64^:controllers/user_profile_controller.go:112+` (ChangeProfileName);
  `9ab4e64^:controllers/user_info_controller.go:106-184` (DeclareEmail);
  `f170607^:controllers/keygen_controller.go` (keygen).
- Live Spigot consumers: HRPAuth-Spigot `src/main/java/com/hrp/auth/proxy/
  RegisterCommand.java:74`, `ForceBindCommand.java:79`, registered in
  `HrpAuthProxy.java:53-54` (checked in this pass).
- Replacement surfaces in this repo at `77fa3a7`: `main.go:113-134,147-200`;
  `services/mbe_timeout.go:13-73`; `services/texture_service.go:519-566`;
  `services/auth_service.go:128-132,179-183,217-228,286-316`.
- Contract records: HA-Contract `yggdrasil-api.md` (§6 note :170-172, §Data
  Consistency), `hrpauth-yggdrasil-business.yaml` (`x-planned-endpoints`),
  `agents.md:60` (roadmap 2.1).
