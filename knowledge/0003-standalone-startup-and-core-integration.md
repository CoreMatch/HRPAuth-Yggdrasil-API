# ADR-0003: Standalone Startup Is Possible; Core Integration Is Opt-In by Credential

- **Status**: accepted
- **Date**: 2026-11-04

## Context

The service must boot even when the HRPAuth core is absent (first-run config
generation, isolated test deployments), yet in production it must register itself
with the core (presence + relay), fetch site metadata, and push its WebUI SDK
package. The design question was how to make the core dependency optional without
forking the code path.

## Decision

Core integration is **switched on solely by filling `core_api.client_id` /
`core_api.client_secret`** in `config.yaml`:

- `utils.GetCoreAPIClient()` builds the OAuth2 client-credentials client only when
  both fields are non-empty; otherwise it logs "Service-to-service calls will be
  disabled" and returns `nil` (`utils/core_api_client.go:L26-L43`).
  Every core-facing startup step nil-checks this client and skips itself:
  metadata fetch (`controllers/startup_controller.go:L586-L590`),
  presence/relay registration
  (`controllers/startup_controller.go:L642-L646`).
- **Startup never hard-fails on core problems.** Metadata fetch failure only warns
  (`main.go:L106-L108`); registration and SDK upload run in a background
  goroutine after a 2-second delay and only warn on failure
  (`main.go:L227-L235`).
- When configured, `RegisterService()` performs three steps against the core
  (`controllers/startup_controller.go:L635-L691`):
  1. `POST /services/presence` with `security_level: 0` — deliberately the lowest
     level "to allow anonymous relay for Yggdrasil"
     (`controllers/startup_controller.go:L658`), scope name
     `yggdrasil-api`, frontend area `webui-dash`, and the Yggdrasil meta blob as
     root metadata;
  2. `POST /services/relay` with 14 path rules mapping
     `<relay_url><path>` → `<relay_path_prefix><path>` (default prefix
     `/yggdrasil-api`; paths listed in `buildRelayRules`,
     `controllers/startup_controller.go:L693-L721`);
  3. `FetchInternalKey()` — obtains the runtime `X-Internal-Key` used by the
     `/internal/*` endpoints (ADR-0005).
- **SDK package upload on every start**: the embedded `sdk/` tree (Go `embed`,
  `main.go:L18-L24`) is packed as `<name>-sdk.tar.gz` and POSTed to
  `/services/sdk-packages`. On HTTP 409 (package name already exists) the old
  package is DELETEd and the upload retried exactly once
  (`controllers/sdk_upload.go:L133-L165`,
  `clients/core_client.go:L302-L360`).
  `manifest.json` profile-card priorities are patched from
  `microservice.profile_cards` config before packing
  (`controllers/sdk_upload.go:L40-L74`).
- **Degraded auth without core**: `VerifyCredentials` tries local proxy accounts
  first and only then the core (`services/auth_service.go:L58-L104`),
  so a standalone instance can still authenticate internally registered proxy/bot
  accounts.

## Consequences

- A fresh install boots into a config-generation flow (default `config.yaml` +
  RSA key pair, `controllers/startup_controller.go:L147-L176`) with
  empty `core_api` credentials; the operator must fill them and restart to join
  the ecosystem. This is the documented deployment path
  (`README.md:L49-L62`).
- Because registration failures are warnings, a misconfigured production service
  "runs" but is invisible to the core relay — the game client reachability breaks
  silently until logs are read. Presence TTL is 120 s
  (`config.yaml:L21`), so a dead registration also expires out of the
  core's view.
- The 409→delete→re-upload policy means **every restart of this service replaces
  the core's stored SDK package** with the build-embedded one. This keeps the
  WebUI pages in lockstep with the running service version, but any SDK package
  hand-edited on the core side is silently overwritten.
- CORS origin and meta links derive from the core's site metadata
  (`main.go:L28-L36`,
  `controllers/yggdrasil_controller.go:L108-L117`); until the first
  successful metadata fetch they can be wrong (the startup warning says so,
  `main.go:L107`).
- `security_level: 0` is intentional: Yggdrasil endpoints must be reachable by game
  clients that have no HRPAuth session. The trade-off is that all relayed
  Yggdrasil paths are anonymously reachable and must defend themselves (rate
  limiting, token checks) — see [xref] HA-Contract
  `docs/dev/HRPAuth/microservices.md` for the level model.

## Evidence

All paths relative to this repository unless noted.

- Opt-in client construction: `utils/core_api_client.go:L26-L43`.
- Startup sequence and warn-only degradation: `main.go:L90-L239`.
- Presence registration (level 0 comment, scope, root metadata):
  `controllers/startup_controller.go:L653-L671`.
- Relay rule builder and default prefix:
  `controllers/startup_controller.go:L693-L721`;
  `config/config.go:L144` (`DefaultRelayPathPrefix = "/yggdrasil-api"`).
- Internal key fetch: `controllers/startup_controller.go:L608-L633`.
- SDK embed and upload trigger: `main.go:L18-L24`,
  `main.go:L233-L234`.
- 409 conflict → delete → single retry:
  `controllers/sdk_upload.go:L148-L162`,
  `clients/core_client.go:L330-L332` (conflict mapping),
  `clients/core_client.go:L342-L360` (delete).
- Manifest priority patching: `controllers/sdk_upload.go:L40-L74`;
  priorities config `config.yaml:L16-L18`.
- Proxy-first credential verification:
  `services/auth_service.go:L58-L104`.
- SDK package format and broker role of the core: [xref] HA-Contract
  `docs/dev/HRPAuth/sdk-package.md` ("Design Positioning", "Data Flow") —
  checked in this pass.
