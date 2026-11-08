# ADR-0004: Session Upsert and Query-Time Expiry Renewal

- **Status**: accepted

## Context

The Yggdrasil join/hasJoined handshake is how a game server verifies a connecting
player. Early in the project's history the session expiry was very short, which
broke long-lived players (skins/verification dropping while online). The session
semantics were therefore tuned for "player stays connected" rather than "strict
one-shot ticket", and the expiry must stay operator-tunable
(`yggdrasil.security.session_expiry_seconds`, default 28800 = 8 h,
`config.yaml:L53-L53 fp=4f5dcadd ~ session_expiry_seconds: 28800`).

## Decision

Session semantics in `services/auth_service.go`:

- **Join is an upsert keyed by (profile_id, server_id).** If a row for that pair
  exists, its `ip` and `expires_at` are updated in place; otherwise a new row is
  inserted (`CreateSession`, `services/auth_service.go::CreateSession`).
  A player on a given server therefore never accumulates more than one session
  row.
- **hasJoined does not check `expires_at` at read time.** It looks the session up
  by (profile, server) and, on hit, force-renews `expires_at` to
  now + `session_expiry_seconds` before returning it
  (`GetSessionByProfileAndServer`,
  `services/auth_service.go::GetSessionByProfileAndServer`). The HTTP handler itself also
  performs no expiry comparison
  (`controllers/yggdrasil_controller.go::HasJoined`); a miss yields
  `204` per the Yggdrasil convention
  [xref] (HA-Contract `docs/dev/HRPAuth/yggdrasil-api.md`).
- **Expiry only matters for garbage collection**: a background job every 24 h
  deletes rows whose `expires_at` is more than 24 h in the past
  (`CleanupExpiredSessions`, `services/auth_service.go::RecordLoginAttempt`;
  scheduled in `main.go::main`).
- The optional IP binding check (`enable_ip_check`) is off by default
  (`config.yaml:L41-L41 fp=9cc042f2 ~ enable_ip_check: false`) and, when enabled, compares the stored join IP
  with the `ip` query parameter
  (`controllers/yggdrasil_controller.go::HasJoined`).

## Consequences

- **Effective session lifetime is `session_expiry_seconds` + up to 24 h of grace**,
  measured from the last join/hasJoined — not from the original join. Any hasJoined
  within the window revives and extends the session; even a row whose `expires_at`
  has passed is renewed if the cleanup job has not removed it yet. Operators who
  expect `session_expiry_seconds` to be a hard TTL will be surprised: it is a
  cleanup threshold, not a read-time gate.
- The design deliberately trades strictness for robustness: reconnection storms,
  proxy restarts, or slow skin fetches cannot invalidate an honest player's
  session, and one row per (profile, server) keeps the table bounded.
- Cost: a leaked/stolen (username, serverId) pair stays usable until cleanup
  removes the row, and there is no per-read audit of staleness. The IP check exists
  as a mitigation but is disabled by default.
- hasJoined writes to the database on every successful verification (the renewal
  `Save`), so session-server query traffic produces write load.

## Evidence

All paths relative to this repository unless noted.

- Upsert: `services/auth_service.go::CreateSession` (log fields
  `mode=renew`/`mode=new`).
- Read without expiry filter + forced renewal:
  `services/auth_service.go::GetSessionByProfileAndServer`.
- Handler performs no expiry comparison, returns 204 on miss:
  `controllers/yggdrasil_controller.go::HasJoined`.
- Cleanup threshold (expired > 24 h) and schedule:
  `services/auth_service.go::RecordLoginAttempt`,
  `main.go::main`.
- Expiry setting and default: `config.yaml:L53-L53 fp=4f5dcadd ~ session_expiry_seconds: 28800`; default also in
  `controllers/startup_controller.go::buildDefaultConfig`.
- IP check flag and default off: `config.yaml:L41-L41 fp=9cc042f2 ~ enable_ip_check: false`,
  `controllers/yggdrasil_controller.go::HasJoined`.
- 204-on-failure convention: [xref] HA-Contract
  `docs/dev/HRPAuth/yggdrasil-api.md`, Sessionserver section — checked in this
  pass.
- Motivation (short expiry broke long-lived players): recorded in project working
  notes from the session-tuning sessions; the original 30-second-expiry incident is
  not verifiable from this repository's current code — not verified.
