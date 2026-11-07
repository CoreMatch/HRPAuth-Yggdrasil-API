# ADR-0005: Internal Surface Authentication and Known Leftovers

- **Status**: accepted

## Context

Besides the public Yggdrasil surface, the service exposes two machine-to-machine
endpoints under `/internal/*` (proxy-account registration and heartbeat) that the
core or tooling may call. Separately, a few residual facts about the repository
are easily misread; maintainers need a list of what they are.

## Decision / Findings

**Internal surface authentication.** `/internal/proxy-register` and
`/internal/heartbeat` are guarded solely by an `X-Internal-Key` header compared
against `config.AppConfig.Runtime.InternalKey`
(`controllers/internal_controller.go:L23-L37 fp=2e14d138 ~ func (ic *InternalController) Heartbeat(c *gin.C`). The key is not
configured by hand: it is fetched from the core's `POST /services/auth-key`
during service registration
(`controllers/startup_controller.go::FetchInternalKey`). When the core is not
configured, the runtime key stays empty and the guard's `InternalKey == ""`
branch makes both endpoints reject every request with 401 — internal endpoints
are therefore inert in standalone mode, by construction.

**Easily misread points:**

1. **This service does not host a skin library.** Public skin browsing lives in
   the WebUI's first-party Skinlib page backed by HASkinLib; nothing in this
   repository serves it.
2. **Secrets remain retrievable from git history.** The signing key pair
   (`private_key.pem` / `public_key.pem`) is assessed in ADR-0009: never used
   by any code, no rotation needed. The `sshpass`-based deployment command
   (production host credentials) that `build.sh` carried is likewise still in
   history and must be considered exposed; whether it was rotated is not
   verifiable from this repository — flagged for owner confirmation. The
   current signing key pair is generated locally at first run and is not in
   the tree (`controllers/startup_controller.go::createDefaultConfig`).
3. **Migration versions above the highest embedded migration do not correspond
   to files in the current tree** (the migration set is squashed, see ADR-0002);
   `EnsureMigrations` clamps such databases to the highest embedded version
   (`controllers/startup_controller.go::EnsureMigrations`).

## Consequences

- The internal key rotates implicitly whenever registration re-fetches it; there
  is no manual key configuration to drift out of sync, but a service that registered
  long ago and whose core-side key changed will fail 401 until it re-registers
  (restart). Key lifetime on the core side: not verified from this repository.
- The history-resident deployment password is an open risk item for the owner
  (rotation cannot be verified from this repository); the history-resident
  signing key pair is closed per ADR-0009 (never used, no rotation needed).

## Evidence

All paths relative to this repository unless noted.

- Internal key guard: `controllers/internal_controller.go:L23-L37 fp=2e14d138 ~ func (ic *InternalController) Heartbeat(c *gin.C`;
  routes registered in `main.go::main`.
- Key fetch from core: `controllers/startup_controller.go::FetchInternalKey`
  (`POST /services/auth-key`).
- Secret removal from the working tree: commit `3517456` (deleted the committed
  key pair and the hardcoded deployment command from `build.sh`); both remain
  retrievable from history. Key-pair assessment: ADR-0009.
- Runtime key generation at first run:
  `controllers/startup_controller.go::createDefaultConfig`.
- Post-squash clamp: `controllers/startup_controller.go::EnsureMigrations`;
  commit `45000e9`.
