# ADR-0005: Internal Surface Authentication and Known Leftovers

- **Status**: accepted
- **Date**: 2026-11-04

## Context

Besides the public Yggdrasil surface, the service exposes two machine-to-machine
endpoints under `/internal/*` (proxy-account registration and heartbeat) that the
core or tooling may call. Separately, the codebase carries remnants of features that
were added and removed during the fast-moving October 2026 period, plus artifacts
that were once committed and later deleted. Maintainers need a list of what is real
and what is residue.

## Decision / Findings

**Internal surface authentication.** `/internal/proxy-register` and
`/internal/heartbeat` are guarded solely by an `X-Internal-Key` header compared
against `config.AppConfig.Runtime.InternalKey`
(`controllers/internal_controller.go:22-36`). The key is not configured by hand: it
is fetched from the core's `POST /services/auth-key` during service registration
(`controllers/startup_controller.go:609-634`). When the core is not configured, the
runtime key stays empty and the guard's `InternalKey == ""` branch makes both
endpoints reject every request with 401 — internal endpoints are therefore inert in
standalone mode, by construction.

**Known leftovers and easily misread points:**

1. **`GetUserByID` is a dead placeholder.** It returns `nil` unconditionally and its
   comment admits it was never implemented (`services/auth_service.go:687-693`).
   Callers must not rely on it.
2. **`texture_list_skin` / `texture_list_cape` in the migration prefix list are
   ghosts.** The table-prefix rewriter still renames these tables
   (`controllers/startup_controller.go:368-371`), but no migration creates them
   since the texture-library feature was removed (commit `3dd8ec3`, 2026-10-13,
   which dropped the tables; the removal migration was itself absorbed by the
   2026-10-14 squash `fa788be`). Harmless, but misleading when reading the list.
3. **The texture library / "texture discovery" (ex-skinlib) feature no longer
   exists here.** It was implemented on 2026-10-07 (`4e00aed`), reworked on
   2026-10-13 (`6eebf2b`, `3cea9c5`, `8d3a949`, `c2c9e28`), and fully removed the
   same day (`3dd8ec3`, `c43a91c`), including the SDK's TextureDiscovery page.
   Any narrative that this service hosts a skin library is stale; public skin
   browsing lives in the WebUI's first-party Skinlib page backed by HASkinLib.
4. **Secrets were committed once and later removed — they remain in git history.**
   Commit `3517456` (2026-10-14) deleted a committed `private_key.pem` /
   `public_key.pem` pair and removed a hardcoded `sshpass`-based deployment command
   (production host credentials) from `build.sh`. Both are still retrievable from
   history. The current signing key pair is generated locally at first run and is
   not in the tree (`controllers/startup_controller.go:147-176`).
5. **Migration history was squashed** (see ADR-0002): pre-squash migration numbers
   found in old logs or databases (e.g. versions > 1) do not correspond to files in
   the current tree; `EnsureMigrations` clamps such databases to the highest
   embedded version (`controllers/startup_controller.go:454-464`, fix commit
   `45000e9`).

## Consequences

- The internal key rotates implicitly whenever registration re-fetches it; there is
  no manual key configuration to drift out of sync, but a service that registered
  long ago and whose core-side key changed will fail 401 until it re-registers
  (restart). Key lifetime on the core side: not verified from this repository.
- The ghost table names and the dead `GetUserByID` are safe to remove, but this
  entry records them as-is so readers do not assign them meaning.
- The history-resident secrets are an open risk item for the owner: the committed
  RSA pair and the deployment password should be considered exposed and rotated;
  whether that was done is not verifiable from this repository — flagged for owner
  confirmation rather than asserted.

## Evidence

All paths relative to this repository at commit `68feb97` unless noted.

- Internal key guard: `controllers/internal_controller.go:22-36`; routes registered
  in `main.go:149-150`.
- Key fetch from core: `controllers/startup_controller.go:609-634`
  (`POST /services/auth-key`).
- Dead placeholder: `services/auth_service.go:687-693`.
- Ghost table names: `controllers/startup_controller.go:368-371`; table removal in
  commit `3dd8ec3` ("Drop texture_list_skin and texture_list_cape database tables").
- Feature churn chronology: commits `4e00aed` (2026-10-07), `6eebf2b`, `3cea9c5`,
  `8d3a949`, `c2c9e28`, `3dd8ec3`, `c43a91c` (all 2026-10-13).
- Secret removal: commit `3517456` — stat shows `private_key.pem` (-51),
  `public_key.pem` (-14), `build.sh` hunk removing the `sshpass ... scp` line;
  message: "Remove the hardcoded remote deployment command from build.sh".
- Post-squash clamp: `controllers/startup_controller.go:454-464`; commit `45000e9`.
