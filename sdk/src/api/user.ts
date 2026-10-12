import { BackendUrl, request, type ApiResponse } from '../utils/proxy';

export interface MojangBindResponse {
  uid: number;
  mbe: 0 | 1;
}

export async function enableMojangBind(): Promise<ApiResponse<MojangBindResponse>> {
  return request<MojangBindResponse>(`${BackendUrl}/user/mojang-bind-enable`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
  });
}

export async function disableMojangBind(): Promise<ApiResponse<MojangBindResponse>> {
  return request<MojangBindResponse>(`${BackendUrl}/user/mojang-bind-disable`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
  });
}
