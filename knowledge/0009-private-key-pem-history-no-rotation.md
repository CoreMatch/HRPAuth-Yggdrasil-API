# ADR-0009: `private_key.pem` in Git History — Never Used, No Rotation Needed

- **Status**: accepted
- **Date**: 2026-11-05

## Context

A `private_key.pem` file appears in this repository's git history. Secret
material in history normally triggers a rotation review; the author asked for
the exposure to be assessed and recorded, explicitly **without** rewriting git
history (no force-push).

## Findings

- The file was committed once, in `dd749ac` (2026-10-14, "feat: add profile
  cards and refactor components"), together with other local development
  artifacts (`config.yaml`, `public_key.pem`, `generated-sdk.ts.tmp`). It was
  removed again in `3517456` ("chore: remove unused files and simplify build
  script").
- **The author confirmed the key was never used by any code.** The only
  in-code reference to the filename is the default *runtime* signature key
  path (`controllers/startup_controller.go`: `private_key.pem` next to the
  config file), where the service **generates its own fresh key pair** on
  first startup. The committed file was never loaded, deployed, or referenced
  by any configuration that shipped.
- There is therefore **no credential in rotation that derives from this
  file, and no rotation is necessary**.

## Decision

- Record the finding here; treat the historical blob as inert.
- **Do not rewrite git history** (no filter-branch/force-push) — the author
  decided the churn is not justified for an unused key.
- Keep the current guarantee: no private key or passphrase file is tracked or
  present in the working tree. Verified on 2026-11-05 across all five HA
  repositories (HA-Contract, HRPAuth, HRPAuth-Yggdrasil-API, WinnerProxy,
  HRPAuth-Spigot): `git ls-files` shows no `.pem`/`.key`/secret files, and
  all working trees are clean. Runtime-generated keys live outside the
  repositories (next to each deployment's config file).

## Consequences

- Anyone auditing the history will find this entry instead of opening a
  rotation incident.
- Future commits must keep generated key pairs out of version control
  (they are created at first startup in the deployment directory, not in the
  source tree).
