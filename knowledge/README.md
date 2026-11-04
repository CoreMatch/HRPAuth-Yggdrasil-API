# Knowledge Base

## What This Is

The knowledge base and the contracts have different jobs. **A contract describes a
project's own outward behavior**, so that external developers can build their own
projects against it. **The knowledge base is this project's memo to itself**: it
serves self-hosting and maintenance of this project, and it is not meant to make
external projects accommodate this one. It may therefore record implementation
orientation, boundaries, and why things are designed the way they are — but it never
defines outward behavior.

## Order of Authority

For outward behavior, **the HA-Contract repository is authoritative**: the Yggdrasil
protocol surface is documented in HA-Contract `docs/dev/HRPAuth/yggdrasil-api.md`,
the business wrapper endpoints in HA-Contract
`docs/api/openapi/hrpauth-yggdrasil-business.yaml`, and the SDK package / presence /
relay mechanism in HA-Contract `docs/dev/HRPAuth/sdk-package.md` and
`docs/dev/HRPAuth/microservices.md`. This repository keeps no contracts of its own.
The knowledge base only explains internal workings and design rationale. If an entry
here ever appears to contradict a contract, the contract wins and the entry is stale —
fix the entry, never the contract, and surface the conflict to the owner.

## Format

Each entry is one page in ADR style:

- **Status** (`accepted` / `superseded` / ...), **Date**
- **Context** — the problem and constraints
- **Decision** — what was chosen
- **Consequences** — including costs and limitations
- **Evidence** — `file:line`, protocol entries, commit hashes. Every piece of
  evidence must have been actually checked; anything that could not be verified is
  marked "not verified". Do not fabricate.

## Entries

| # | Title | Status | Date |
|---|-------|--------|------|
| [0001](0001-service-boundary-and-split-from-core.md) | Service Boundary: Split From the HRPAuth Core | accepted | 2026-11-04 |
| [0002](0002-game-data-ownership.md) | Game Data Ownership: Six Prefixed Tables and the accounts Bridge | accepted | 2026-11-04 |
| [0003](0003-standalone-startup-and-core-integration.md) | Standalone Startup Is Possible; Core Integration Is Opt-In by Credential | accepted | 2026-11-04 |
| [0004](0004-session-upsert-and-query-time-renewal.md) | Session Upsert and Query-Time Expiry Renewal | accepted | 2026-11-04 |
| [0005](0005-internal-surface-and-known-leftovers.md) | Internal Surface Authentication and Known Leftovers | accepted | 2026-11-04 |
| [0006](0006-decoupling-status-declare-email-and-mojang-bind.md) | Decoupling Status of declare-email and mojang-bind-enable | accepted | 2026-11-04 |
| [0007](0007-decoupling-inheritance-audit.md) | Decoupling Inheritance Audit — What Was Replaced, What Was Lost | accepted | 2026-11-05 |
| [0008](0008-claim-force-bind-implementation.md) | Claim and Force-Bind Implementation; cbh Persistence Fix | accepted | 2026-11-05 |
