# ADR-0002: Game Data Ownership: Six Prefixed Tables and the accounts Bridge

- **Status**: accepted

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
  `profiles`, `profile_properties`, `sessions`, `tokens`
  [xref] (HRPAuth pre-split blob `80bd9cbb`, lines 24/37/48/62), and core
  migration `000004_profile_keys` created `profile_keys`. All five now exist
  only here.
- **`accounts` is the bridge table introduced by the split.** It maps
  `core_user_id` (the core's `users.uuid`) 1:1 fp=0c1614d3 to a game account and carries the
  Mojang-binding and proxy/bot flags (`mbe`, `cbh`) plus the official skin
  sync state (`skin_sync`, `skin_sync_mark`, ADR-0015,
  `database/migrations/000002_account_skin_sync.up.sql`)
  (`database/migrations/000001_init.up.sql:L1-L14 fp=f81da341 ~ CREATE TABLE IF NOT EXISTS `accounts` (`,
  `models/models.go::Account`).
- **`profile_keys` changed its key during the move**: in the core it was keyed by
  `user_id varchar(32)` referencing `users(uuid)`; here it is keyed by
  `account_id int` referencing `accounts(id)`
  (`database/migrations/000001_init.up.sql:L74-L88 fp=fd1ba1e6 ~ CREATE TABLE IF NOT EXISTS `profile_keys` (`). Chat-signing
  keys therefore attach to the game account, not directly to the core user.
- **Table prefixing is applied to migration SQL by string replacement** at startup
  (`controllers/startup_controller.go::wrapReader`); GORM model table
  names use the same prefix (`models/models.go::TableName` etc.).
- The migration set is a single **squashed** `000001_init` baseline (commit
  `fa788be`); databases that still carry a higher pre-squash version number are
  clamped to the highest embedded version by `EnsureMigrations`
  (`controllers/startup_controller.go::EnsureMigrations`, fix commit `45000e9`).

## Consequences

- Operators sharing one MySQL database between core and this service must keep the
  table prefixes distinct (core uses unprefixed names; this service defaults to
  `haygg_`).
- The "Moved from core" comments in `models/models.go` exist only on `Token`,
  `Session` and `ProfileKey` (`models/models.go:L10-L10 fp=fd69500d ~ Moved from core to Yggdrasil API provider.`,
  `models/models.go:L28-L28 fp=fd69500d ~ Moved from core to Yggdrasil API provider.`, `models/models.go:L44-L44 fp=fd69500d ~ Moved from core to Yggdrasil API provider.`).
  `Profile` and `ProfileProperty` were moved too but carry no such comment —
  do not read the missing comment as "always lived here".
- There is no data-migration tooling from the old core tables to these tables in
  this repository; the split shipped fresh tables. Whether production data was
  copied over at cutover time is not recorded in either repository — not verified.
- Cost of the bridge design: every core-user → game lookup goes through
  `accounts.core_user_id`, and deleting a core user requires this service to clean
  up its side (see the cascade-delete handling in
  `services/auth_service.go::CleanupInactiveBotUsers`).

## Evidence

All paths relative to this repository unless noted.

- Six tables and their DDL: `database/migrations/000001_init.up.sql:L1-L88 fp=d666e522 ~ CREATE TABLE IF NOT EXISTS `accounts` (`.
- Default table prefix `haygg_`: `config.yaml:L12-L12 fp=d57d76b2 ~ table_prefix: haygg_`; default config
  builder `controllers/startup_controller.go::buildDefaultConfig`.
- Pre-split core baseline (profiles/profile_properties/sessions/tokens): [xref]
  HRPAuth repository, git blob `80bd9cbb`
  (`git show 9ab4e64^:database/migrations/000001_baseline.up.sql`),
  `CREATE TABLE` at lines 24, 37, 48, 62.
- Pre-split core `profile_keys` keyed by `user_id` → `users(uuid)`: [xref]
  HRPAuth `git show 9ab4e64^:database/migrations/000004_profile_keys.up.sql`.
- Post-split core has no game tables: [xref] HRPAuth
  `database/migrations/000001_baseline.up.sql` (checked in this pass).
- "Moved from core" comments: `models/models.go:L10-L10 fp=fd69500d ~ Moved from core to Yggdrasil API provider.`,
  `models/models.go:L28-L28 fp=fd69500d ~ Moved from core to Yggdrasil API provider.`, `models/models.go:L44-L44 fp=fd69500d ~ Moved from core to Yggdrasil API provider.`.
- Prefix string replacement: `controllers/startup_controller.go::wrapReader`.
- Squash and post-squash clamp: commits `fa788be`, `45000e9`;
  `controllers/startup_controller.go::EnsureMigrations`.
