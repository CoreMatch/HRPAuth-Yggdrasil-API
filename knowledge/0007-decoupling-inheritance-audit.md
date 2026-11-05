# ADR-0007: Decoupling Inheritance Audit — What Was Replaced, What Was Lost

- **Status**: accepted

## Context

The split (ADR-0001) stripped the Minecraft stack out of the HRPAuth core. This
entry maps every capability removed at the split to its current replacement in
the ecosystem, so a missing route in the core is never mistaken for a missing
feature.

## Decision (where each removed capability lives now)

**Replaced in this service (form may differ):**

- Standard texture write paths (`PUT/DELETE /api/user/profile/...`) were
  deliberately not migrated; the business wrappers are the supported surface
  [xref] (HA-Contract `yggdrasil-api.md:170-172`).
- `/texture/rewrite-callback` became read-time lazy rewriting
  (`services/texture_service.go::RewriteTexturesProperty`).
- `/generate-key` became first-run automatic key generation
  (`controllers/startup_controller.go:L147-L176`).
- Runtime SDK endpoints became the compile-time sdk-packages model
  (ADR-0003).
- Core→ygg push sync became lazy pull: usernames are pulled on access
  (`services/auth_service.go:L271-L301`), with one behavioral difference —
  accounts of deleted core users are only cleaned here on next access.
- Mojang-bind completion (username collision + `mbe=1` → bind) lives in
  `ProxyRegister` (`services/auth_service.go:L193-L219`; ADR-0006, ADR-0008).

**Resolved by author decision (ADR-0008):**

- `/admin/claim-user` and `/admin/force-bind` are implemented in this service,
  covering the live HRPAuth-Spigot consumers.
- `change-profile-name` is deliberately not restored as an endpoint; the lazy
  username pull renames the profile (uniqueness conflict → keep old name).
- `declare-email` is confirmed deprecated with no successor; the claim flow
  covers the real-email need (ADR-0006).

**Not decoupling losses:** `POST /login` and `/loginbymt` have no successor and
need none — they were already 410 Gone stubs when the split happened (OAuth2
had replaced them).

## Consequences

- No orphan scope families from the split remain in the core's OAuth2 registry:
  `user.claim.as-service` and `user.force-bind.as-service` are consumed by this
  service's `/admin` endpoints (ADR-0008), and `user.declare-email`,
  `register.manage`, `profile.change-name(.as-service)`,
  `user.mojang-bind-*(.as-service)`, `totp.status(.as-service)` and
  `email-2fa.status(.as-service)` were removed from the registry (deprecated,
  superseded, or never wired to an endpoint).
- The lazy-pull replacement means game accounts of deleted core users persist
  until next access; if that is unacceptable, an explicit deletion signal would
  be a new design item.

## Evidence

All paths relative to this repository unless noted.

- Replacement surfaces: `main.go:L148-L217` (route registration),
  `services/mbe_timeout.go::MBETimeout`,
  `services/texture_service.go::RewriteTexturesProperty`,
  `services/auth_service.go:L271-L301` (lazy pull incl. cleanup of deleted
  core users), `services/auth_service.go:L193-L219` (mbe bind branch).
- Live Spigot consumers of claim/force-bind: [xref] HRPAuth-Spigot `src/main/java/com/hrp/auth/proxy/RegisterCommand.java:74`, `ForceBindCommand.java:79`, registered in `HrpAuthProxy.java:53-54`.
- Contract records: [xref] HA-Contract `yggdrasil-api.md` (§6 note
  :170-172, §Data Consistency), `hrpauth-yggdrasil-business.yaml`
  (`/admin` endpoints), `hrpauth-business.yaml:232-247` (declare-email
  deprecation).
