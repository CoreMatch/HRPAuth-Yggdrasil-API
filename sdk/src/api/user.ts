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
