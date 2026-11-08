# ADR-0015: Official Skin Auto-Sync After Mojang Bind

- **Status**: accepted

## Context

Binding a genuine Mojang identity (`accounts.mojang_uuid`) never touched
the profile textures: the bind paths only wrote the uuid
(`services/auth_service.go::ProxyRegister`,
`services/admin_service.go::ClaimProxyAccount`,
`services/admin_service.go::ForceBindTransfer`), so a bound player kept
whatever skin their profile had before — usually none, i.e. the default
Steve/Alex. The `GET /texture/mojang/{uuid}` redirect
(`controllers/player_controller.go::Texture`) only serves the SDK skin
preview (302 to textures.minecraft.net) and writes nothing locally, which
made the gap invisible in the account UI. SamuelClient ADR-0007 lists this
as open question OQ-3 [xref] (`/home/lnb/Desktop/HA/SamuelClient/knowledge/0007-microsoft-login-and-ha-account-binding-design.md`);
the author decision implemented here: **mirror the official skin
automatically**, with a per-account opt-out.

## Decision

- **Trigger points (service layer).** Every bind completion schedules a
  sync for the affected account: `services/auth_service.go::ProxyRegister`
  (all three success branches — new proxy account, mbe-window bind,
  idempotent return of an already-bound account),
  `services/auth_service.go::RegisterGameAccount` (only when a
  `mojang_uuid` is supplied), `services/admin_service.go::ClaimProxyAccount`
  and `services/admin_service.go::ForceBindTransfer` (target account).
  The future client direct-bind endpoint (`POST /player/microsoft-bind`,
  gated on Azure approval [xref]) must call the same hook
  (`services/skin_sync.go::RequestSkinSync`) when it lands.
- **Retry path.** A failed or missed sync is retried on the next Yggdrasil
  login (`controllers/yggdrasil_controller.go::skinSyncRetryOnLogin` in
  `Authenticate`) and on any later bind of the same account (the
  idempotent proxy-register branch is the join-time retry for proxy
  players). `POST /player/skin-sync-enable` doubles as a manual retry.
- **Idempotency mark.** `accounts.skin_sync_mark`
  (`database/migrations/000002_account_skin_sync.up.sql`) records the last
  successful sync as `<mojang_uuid>:<official texture hash|default>`. A
  mark covering the bound uuid short-circuits before any Mojang call, so
  steady-state logins and repeated proxy registrations cost one
  primary-key read. The mark pins the Mojang-side hash (parsed from the
  official texture URL by `services/skin_sync.go::officialTextureHash`),
  not the locally re-encoded hash. Consequence: an upstream skin *change*
  is not re-mirrored until the mark is cleared (disable→enable); this is
  accepted, the feature mirrors bind events, not upstream churn.
- **Apply pipeline.** The mirror reuses the manual-upload storage path:
  download (size-capped), `services/texture_service.go::ValidateTexture`,
  store PNG + face avatar + WebP preview, then
  `services/texture_service.go::UpdateProfileTexture` (tombstone + re-sign)
  for every profile of the account. The official `slim` model metadata is
  carried through. Capes are **not** mirrored (scope: skins only).
- **No-skin case.** An official profile without a SKIN texture clears any
  local skin binding (`services/texture_service.go::removeProfileTextureTx`
  under the storage lock), so the client renders the default skin; the
  mark records `<uuid>:default`.
- **Failure degradation.** Sync runs in a goroutine
  (`services/skin_sync.go::skinSyncDispatch`); no bind request ever waits
  for or fails because of Mojang. Every outcome is logged as
  `[YGG-SKINSYNC] account=… trigger=… result=…` with exactly one of the
  `SkinSyncResult*` constants (`services/skin_sync.go`): `synced`,
  `up_to_date`, `no_official_skin`, `disabled`, `not_bound`, `no_profile`,
  `upstream_unreachable` (network/timeout/5xx, including the texture
  download), `upstream_not_found` (404/204), `texture_invalid`,
  `apply_failed`. Failures leave the mark unchanged, which is what makes
  the login retry pick them up.
- **Per-account toggle.** `accounts.skin_sync` defaults to 1 (migration
  backfills existing rows). Self-service endpoints
  `POST /player/skin-sync-enable|disable`
  (`services/player_service.go::EnableSkinSync`,
  `services/player_service.go::DisableSkinSync`, same token resolution as
  the mbe toggle) mirror the mojang-bind-enable/disable interaction, and
  the SDK renders a matching switch in the Mojang bind card
  (`sdk/src/components/MojangBindContent.tsx`). Disable clears the mark,
  so re-enabling always re-syncs; while disabled, no bind or login touches
  the profile textures. `GET /player` and the toggle responses report the
  state in `skin_sync` (`services/player_service.go::PlayerInfo`).
- **Contract.** Registered first as HA-Contract
  `docs/api/openapi/hrpauth-yggdrasil-business.yaml` version 1.1.0
  (two toggle endpoints, `skin_sync` in the player summary, sync notes on
  `/register`, `/internal/proxy-register`, `/admin/claim-user`,
  `/admin/force-bind`), then implemented here; drift verified with
  `scripts/check-business-api-drift.sh`.

## Consequences

- Bound players see their official skin in-game immediately after any bind
  path, without any client change; unbound accounts are untouched
  (`skin_sync` without a `mojang_uuid` never acts).
- Each first bind of an account costs one sessionserver call plus one
  textures.minecraft.net download, off-request; steady state adds no
  Mojang traffic.
- A manually uploaded skin of a bound account is overwritten by the mirror
  while the toggle is on — that is the semantics of "sync is the default";
  the toggle is the escape hatch.
- Sync state is per account, not per profile; all profiles of an account
  receive the same mirrored skin.

## Evidence

- Implementation: this repository, branch
  `feat/mojang-skin-sync-20261108` (contract HA-Contract `c23c22d`).
- Tests: `services/skin_sync_test.go` (apply/idempotency, upstream
  unreachable, upstream 5xx, no-official-skin clearing, toggle-off
  protection, all bind entry-point triggers, toggle endpoints, login-retry
  predicate, invalid texture), plus the pre-existing bind suites;
  `go build/vet/test -race` green.
