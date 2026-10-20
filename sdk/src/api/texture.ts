import { BackendUrl, buildServiceUrl, request, type ApiResponse } from '../utils/proxy';

export async function applyTextureToUser(
  type: string,
  file: File,
  model?: string,
  uid?: number
): Promise<ApiResponse> {
  const url = buildServiceUrl(BackendUrl, '/texture/upload');
  const formData = new FormData();
  formData.append('texture_type', type);
  formData.append('file', file);
  if (model) formData.append('model', model);
  if (uid) formData.append('uid', uid.toString());

  return request(url, {
    method: 'POST',
    body: formData,
  });
}

// SkinViewer3D 通过 <img> 直接拉这个 URL；后端 GET /texture/mojang/:uuid 会 302 到 textures.minecraft.net。
export function mojangTextureUrl(
  uuid: string,
  type: 'skin' | 'cape' = 'skin',
): string {
  return buildServiceUrl(BackendUrl, `/texture/mojang/${uuid}?type=${type}`);
}

export interface MojangProfileResponse {
  id: string;
  name: string;
  has_cape: boolean;
}

// fetchMojangProfile 走本仓库 /yggdrasil-api/mojang/profile/:uuid，
// 由后端解析 sessionserver 返回的 textures property 并下发 has_cape。
export async function fetchMojangProfile(
  uuid: string,
): Promise<MojangProfileResponse | null> {
  const url = buildServiceUrl(BackendUrl, `/mojang/profile/${uuid}`);
  const resp = await fetch(url, { credentials: 'include' });
  if (!resp.ok) return null;
  const body = await resp.json().catch(() => null);
  if (!body?.success || !body?.id) return null;
  return { id: body.id, name: body.name, has_cape: body.has_cape };
}

export async function fetchUserTextures(): Promise<ApiResponse> {
  const url = buildServiceUrl(BackendUrl, '/texture/get');
  return request(url, {
    method: 'POST',
  });
}
