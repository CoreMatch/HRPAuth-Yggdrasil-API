import { BackendUrl, request, type ApiResponse } from '../utils/proxy';

export async function applyTextureToUser(
  type: string,
  file: File,
  model?: string,
  uid?: number
): Promise<ApiResponse> {
  const url = `${BackendUrl}/texture/upload`;
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

export function mojangTextureUrl(
  uuid: string,
  type: 'skin' | 'cape' = 'skin',
): string {
  return `${BackendUrl}/texture/mojang/${uuid}?type=${type}`;
}

export interface MojangProfileResponse {
  id: string;
  name: string;
  has_cape: boolean;
}

export async function fetchMojangProfile(
  uuid: string,
): Promise<MojangProfileResponse | null> {
  const resp = await fetch(`${BackendUrl}/mojang/profile/${uuid}`);
  if (!resp.ok) return null;
  const body = await resp.json().catch(() => null);
  if (!body?.success || !body?.id) return null;
  return { id: body.id, name: body.name, has_cape: body.has_cape };
}

export async function fetchUserTextures(): Promise<ApiResponse> {
  const url = `${BackendUrl}/texture/get`;
  return request(url, {
    method: 'POST',
  });
}

