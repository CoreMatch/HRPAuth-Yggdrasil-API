# ADR-0003: Standalone Startup Is Possible; Core Integration Is Opt-In by Credential

- **Status**: accepted

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
- **Startup hard-fails only on credential resolution; other core problems
  stay warn-only.** Metadata fetch failure only warns
  (`main.go:L116-L118`); registration and SDK upload run in a background
  goroutine after a 2-second delay and only warn on failure
  (`main.go:L238-L246`). Credential resolution (next item) runs right after
  `config.Load()` and aborts startup on any failure
  (`main.go:L99-L107`).
- **Credential resolution** (`main.go::resolveCoreCredentials`): the
  configured `core_api` credentials are classified at startup via HRPAuth's
  RFC 7591 protected registration endpoint `POST /oauth/register`
  (`clients/selfregister.go::ResolveServiceCredentials`), which only
  authenticates the built-in super client —
  - `201`: super credentials; exchanged for this service's own
    low-privilege client (`client_id` = `microservice.name`, the same name
    registered in presence; stable secret via HRPAuth get-or-create). The
    super secret is discarded from memory and the config file rewritten
    (`config/configwrite.go::RewriteClientCredentials`: atomic temp-file +
    rename, `.bak`, 0600, re-parse validation, comment-preserving
    yaml.Node surgery).
  - `400 invalid_client_metadata`: super credentials but registration
    rejected (presence missing, scope not allowed) — startup aborts.
  - `401`: not super; a plain `client_credentials` token request verifies
    the pair is an ordinary service client, reused as-is (idempotent path
    — no exchange, no write-back).
  The presence bonjour goes first as a token-less plain request
  (`clients/selfregister.go::RegisterPresencePlain`), because registration
  requires a presence-registered name and no super-derived token may ride
  along on anything but the exchange itself. Entrypoint-rendered
  deployments (`config/configwrite.go::IsRenderedDeployment`:
  `CONFIG_TEMPLATE` + `RENDERED_CONFIG`, the HA container convention) are
  only warned about — their config is re-rendered every start, so the
  template/environment side must carry the credentials. A failed
  write-back is not fatal (ERROR log; next startup exchanges again).
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
  `clients/core_client.go:L312-L341`).
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
- Startup sequence and warn-only degradation: `main.go:L92-L250`.
- Credential resolution / super exchange:
  `clients/selfregister.go::ResolveServiceCredentials`,
  `clients/selfregister.go::RegisterPresencePlain`; startup wiring
  `main.go::resolveCoreCredentials`; write-back
  `config/configwrite.go::RewriteClientCredentials`,
  rendered-deployment detection `config/configwrite.go::IsRenderedDeployment`
  (env convention set by `docker-entrypoint.sh`).
- Presence registration (level 0 comment, scope, root metadata):
  `controllers/startup_controller.go:L653-L671`.
- Relay rule builder and default prefix:
  `controllers/startup_controller.go:L693-L721`;
  `config/config.go:L144` (`DefaultRelayPathPrefix = "/yggdrasil-api"`).
- Internal key fetch: `controllers/startup_controller.go:L608-L633`.
- SDK embed and upload trigger: `main.go:L18-L24`,
  `main.go:L244-L245`.
- 409 conflict → delete → single retry:
  `controllers/sdk_upload.go:L148-L162`,
  `clients/core_client.go:L312-L313` (conflict mapping),
  `clients/core_client.go:L324-L341` (delete).
- Manifest priority patching: `controllers/sdk_upload.go:L40-L74`;
  priorities config `config.yaml:L16-L18`.
- Proxy-first credential verification:
  `services/auth_service.go:L58-L104`.
- SDK package format and broker role of the core: [xref] HA-Contract
  `docs/dev/HRPAuth/sdk-package.md` ("Design Positioning", "Data Flow") —
  checked in this pass.
