import { BackendUrl, buildServiceUrl, request, type ApiResponse } from '../utils/proxy';

// 解耦后 Game Account (Mojang bind) 字段已迁到本仓库 (yggdrasil-api)，
// 所有 /player/* 调用走 buildServiceUrl 拼接 /yggdrasil-api 前缀。
const PLAYER_BASE = '/player';

export interface PlayerInfo {
  uid: string;
  account_id: number;
  mbe: boolean;
  mojang_uuid: string;
  cbh: boolean;
}

export interface GameProfile {
  id: string;
  name: string;
  model?: string;
}

export type ProfileCreationMode = 'random' | 'custom' | 'mojang';

export async function getPlayerInfo(): Promise<ApiResponse<PlayerInfo>> {
  return request<PlayerInfo>(buildServiceUrl(BackendUrl, PLAYER_BASE), {
    method: 'GET',
  });
}

export async function enableMojangBind(): Promise<ApiResponse<PlayerInfo>> {
  return request<PlayerInfo>(buildServiceUrl(BackendUrl, `${PLAYER_BASE}/mojang-bind-enable`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
  });
}

export async function disableMojangBind(): Promise<ApiResponse<PlayerInfo>> {
  return request<PlayerInfo>(buildServiceUrl(BackendUrl, `${PLAYER_BASE}/mojang-bind-disable`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
  });
}

export async function getGameProfile(): Promise<ApiResponse<{ profile: GameProfile | null }>> {
  return request<{ profile: GameProfile | null }>(
    buildServiceUrl(BackendUrl, `${PLAYER_BASE}/profile`),
    { method: 'GET' },
  );
}

export async function createGameProfile(input: {
  mode: ProfileCreationMode;
  uuid?: string;
  username?: string;
}): Promise<ApiResponse<GameProfile>> {
  return request<GameProfile>(buildServiceUrl(BackendUrl, `${PLAYER_BASE}/profile`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
}
