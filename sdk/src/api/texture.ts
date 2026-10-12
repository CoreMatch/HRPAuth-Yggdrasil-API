import { BackendUrl, request, type ApiResponse } from '../utils/proxy';
import type { 
  TextureUploadRequest, 
  TextureItem
} from '../types/texture';

export async function applyTextureToUser(
  uuid: string,
  type: string,
  file: File,
  model?: string,
): Promise<ApiResponse> {
  const url = `${BackendUrl}/texture/upload/${uuid}`;
  const formData = new FormData();
  formData.append('type', type);
  formData.append('file', file);
  if (model) formData.append('model', model);

  return request(url, {
    method: 'POST',
    body: formData,
  });
}

export async function deleteTexture(
  uuid: string,
  type: 'skin' | 'cape',
): Promise<ApiResponse> {
  const url = `${BackendUrl}/texture/delete/${uuid}/${type}`;
  return request(url, {
    method: 'DELETE',
  });
}

export async function fetchUserTextures(): Promise<ApiResponse> {
  const url = `${BackendUrl}/texture/get`;
  return request(url, {
    method: 'POST',
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

export interface TextureDeleteRequest {
  type: 'skin' | 'cape';
  hash: string;
}

