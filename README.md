# HRPAuth-Yggdrasil-API

[中文文档](README.zh-CN.md)

HRPAuth-Yggdrasil-API is a Yggdrasil-compatible Minecraft authentication service in the HRPAuth ecosystem. It serves the standard Yggdrasil protocol surface to game clients (compatible with [authlib-injector](https://yushijinhun.github.io/authlib-injector/)), and connects to a running [HRPAuth](https://github.com/CoreMatch/HRPAuth) core for account verification and ecosystem integration.

## Prerequisites

- Go 1.26.4+ (to build from source) or Docker
- MySQL — create the database and user first; tables are created automatically on startup
- Redis
- A running [HRPAuth](https://github.com/CoreMatch/HRPAuth) core — required at startup whenever `core_api` credentials are configured (startup credential resolution aborts on any failure; production configs always set credentials), and required at runtime for regular user authentication and WebUI integration. Only with `core_api` credentials left empty can the service start without a core, and then only internally registered proxy accounts can authenticate.

## Quick Start (binary)

```bash
# 1. Build
./build.sh                    # produces build/HAygg

# 2. First run: generates config.yaml and an RSA signing key pair next to the binary
./build/HAygg

# 3. Stop it, edit config.yaml (see Configuration below), then start again
./build/HAygg

# 4. Verify
curl http://localhost:2770/
# expect JSON containing "status": "online"
```

The service listens on port `2770` by default (`server.port` in `config.yaml`). The configuration file and the generated key pair must stay in the same directory as the binary.

## Quick Start (Docker)

The image is published on Docker Hub: [`limeight/ha-hrpauth-yggdrasil-api`](https://hub.docker.com/r/limeight/ha-hrpauth-yggdrasil-api) (`linux/amd64` + `linux/arm64` multi-arch; tag `1.0.0-7ab0d29` alongside `latest`, effective reference pinned by the manifest list digest in [`deploy-manifest.yaml`](deploy-manifest.yaml)):

```bash
docker run -d --name yggdrasil-api \
  -p 2770:2770 \
  -v /path/to/config.yaml:/app/config.yaml \
  -v ygg-storage:/app/storage \
  limeight/ha-hrpauth-yggdrasil-api@sha256:9c78eddf66dd493aa2cdd77193ba52e5e85fcb378edeec45d0425e5a551c2f59
```

- The image ships **without** a `config.yaml`; mount yours at `/app/config.yaml` (a first run with a mounted writable directory can also generate one).
- Alternatively, set `CONFIG_TEMPLATE` and `RENDERED_CONFIG` to render the configuration from an envsubst template at container start (see `docker-entrypoint.sh`).
- Textures are stored under `/app/storage`.
- The repository Dockerfile remains for local builds (`docker build -t hrpauth-yggdrasil-api .`); deployment references should use the published image.

## Configuration (required fields)

On first start, `config.yaml` is generated with defaults. The fields you normally must change before production use:

| Field | Meaning |
|---|---|
| `core_api.base_url` | Address of the HRPAuth core, e.g. `http://localhost:2778` |
| `core_api.client_id` / `core_api.client_secret` | OAuth2 credentials for the core's service API |
| `database.host` / `db_name` / `user` / `password` | MySQL connection (the database must already exist) |
| `redis.host` / `redis.port` | Redis connection |
| `microservice.relay_url` | The URL of this service as reachable from the HRPAuth core |
| `callback.url` | Public base URL used in texture URLs; if left empty it is derived from the core's site metadata |

For the full field list, see [`config.yaml`](config.yaml) and [`config/config.go`](config/config.go).

## authlib-injector compatibility

The service implements the Yggdrasil protocol and is compatible with authlib-injector: point the injector (or any Yggdrasil client) at the public URL of this service. Serving over HTTPS in production is recommended. If `yggdrasil.server.skin_domains` is left empty, it defaults to the site domain.

## API reference

The API contract is maintained in [HA-Contract](https://github.com/CoreMatch/HA-Contract):

- Yggdrasil protocol surface: [`docs/dev/HRPAuth/yggdrasil-api.md`](https://github.com/CoreMatch/HA-Contract/blob/main/docs/dev/HRPAuth/yggdrasil-api.md)
- Business endpoints (OpenAPI): [`docs/api/openapi/hrpauth-yggdrasil-business.yaml`](https://github.com/CoreMatch/HA-Contract/blob/main/docs/api/openapi/hrpauth-yggdrasil-business.yaml)

## WebUI SDK

This repository embeds a WebUI SDK package that is uploaded to the HRPAuth core when the service starts. For the SDK package format and mounting contract, see [`docs/dev/HRPAuth/sdk-package.md`](https://github.com/CoreMatch/HA-Contract/blob/main/docs/dev/HRPAuth/sdk-package.md).

## Deployment facts

Authoritative deployment facts live in [`deploy-manifest.yaml`](deploy-manifest.yaml)
at the repo root (consumed by HA-Services-Deployer's orchestrator;
`manager: aapanel` means the process is panel-managed and the orchestrator
treats it as read-only):

- Listens on `:2770` (public); health probe `GET /health`.
- Depends on MySQL, Redis and the HRPAuth core (see Prerequisites).
- Config file `config.yaml` (template version 4); database migrations run
  automatically on startup.
- Data dir: `./storage` (textures/previews; shared storage with HASkinLib).

## License

[AGPL-3.0](LICENSE)
