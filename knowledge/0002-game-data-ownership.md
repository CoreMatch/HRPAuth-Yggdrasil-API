# ADR-0002: Game Data Ownership: Six Prefixed Tables and the accounts Bridge

- **Status**: accepted
- **Date**: 2026-11-04

## Context

When the Minecraft stack moved out of the core (ADR-0001), the game-side database
tables had to move with it, because the core kept no game logic. A common shorthand
says "three tables moved out" (tokens, sessions, profile_keys). That shorthand is
incomplete and this entry records the actual state.

## Decision

This service owns **six** MySQL tables, all created from the single embedded
migration `database/migrations/000001_init.up.sql` and all prefixed at runtime with
`database.table_prefix` (default `haygg_`):

| Table | Origin |
|---|---|
| `tokens` | moved from core (was in core baseline) |
| `sessions` | moved from core (was in core baseline) |
| `profiles` | moved from core (was in core baseline) |
| `profile_properties` | moved from core (was in core baseline) |
| `profile_keys` | moved from core, **re-keyed** (see below) |
| `accounts` | **new in this service** — no such table existed in the core |

Key points:

- **Five tables moved, not three.** The core's pre-split baseline migration created
  `profiles`, `profile_properties`, `sessions`, `tokens` (HRPAuth pre-split blob
  `80bd9cbb`, lines 24/37/48/62), and core migration `000004_profile_keys` created
  `profile_keys`. All five now exist only here.
- **`accounts` is the bridge table introduced by the split.** It maps
  `core_user_id` (the core's `users.uuid`) 1:1 to a game account and carries the
  Mojang-binding and proxy/bot flags (`mbe`, `cbh`)
  (`database/migrations/000001_init.up.sql:1-14`, `models/models.go:63-79`).
- **`profile_keys` changed its key during the move**: in the core it was keyed by
  `user_id varchar(32)` referencing `users(uuid)`; here it is keyed by
  `account_id int` referencing `accounts(id)`
  (`database/migrations/000001_init.up.sql:74-88`). Chat-signing keys therefore
  attach to the game account, not directly to the core user.
- **Table prefixing is applied to migration SQL by string replacement** at startup
  (`controllers/startup_controller.go:337-382`); GORM model table names use the same
  prefix (`models/models.go:23-25` etc.). The replacement list still names
  `texture_list_skin`/`texture_list_cape`, which no migration creates anymore — a
  leftover recorded in ADR-0005.
- The migration history was **squashed** into the single `000001_init` baseline on
  2026-10-14 (commit `fa788be`); databases that had run the pre-squash series needed
  the version-clamp fix of commit `45000e9` (2026-10-19), which is now built into
  `EnsureMigrations` (`controllers/startup_controller.go:454-464`).

## Consequences

- Operators sharing one MySQL database between core and this service must keep the
  table prefixes distinct (core uses unprefixed names; this service defaults to
  `haygg_`).
- The "Moved from core" comments in `models/models.go` exist only on `Token`,
  `Session` and `ProfileKey` (lines 10, 28, 44). `Profile` and `ProfileProperty`
  were moved too but carry no such comment — do not read the missing comment as
  "always lived here".
- There is no data-migration tooling from the old core tables to these tables in
  this repository; the split shipped fresh tables. Whether production data was
  copied over at cutover time is not recorded in either repository — not verified.
- Cost of the bridge design: every core-user → game lookup goes through
  `accounts.core_user_id`, and deleting a core user requires this service to clean
  up its side (see the cascade-delete handling in
  `services/auth_service.go:271-284`).

## Evidence

All paths relative to this repository at commit `68feb97` unless noted.

- Six tables and their DDL: `database/migrations/000001_init.up.sql:1-88`.
- Default table prefix `haygg_`: `config.yaml:12`; default config builder
  `controllers/startup_controller.go:94`.
- Pre-split core baseline (profiles/profile_properties/sessions/tokens): HRPAuth
  repository, git blob `80bd9cbb` (`git show 9ab4e64^:database/migrations/000001_baseline.up.sql`),
  `CREATE TABLE` at lines 24, 37, 48, 62.
- Pre-split core `profile_keys` keyed by `user_id` → `users(uuid)`: HRPAuth
  `git show 9ab4e64^:database/migrations/000004_profile_keys.up.sql`.
- Post-split core has no game tables: HRPAuth
  `database/migrations/000001_baseline.up.sql` (checked in this pass).
- "Moved from core" comments: `models/models.go:10,28,44`.
- Prefix string replacement (incl. stale `texture_list_*` names):
  `controllers/startup_controller.go:368-379`.
- Squash and post-squash clamp: commits `fa788be`, `45000e9`;
  `controllers/startup_controller.go:454-464`.
