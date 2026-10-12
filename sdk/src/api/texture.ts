import { BackendUrl, SkinlibUrl, request, type ApiResponse } from '../utils/proxy';
import type { 
  TextureUploadRequest, 
  TextureItem,
  TextureListRequest,
  TextureListResponse,
  TextureDeleteRequest
} from '../types/texture';

export async function uploadTexture(
  data: TextureUploadRequest
): Promise<ApiResponse<TextureItem>> {
  const url = `${SkinlibUrl}/texture/upload`;
  const formData = new FormData();
  formData.append('uid', data.uid.toString());
  formData.append('type', data.type);
  formData.append('name', data.name);
  if (data.model) formData.append('model', data.model);
  if (data.description) formData.append('description', data.description);
  if (data.tags) formData.append('tags', data.tags);
  formData.append('file', data.file);

  return request<TextureItem>(url, {
    method: 'POST',
    body: formData,
  });
}

export async function listTextures(
  params: TextureListRequest = {}
): Promise<ApiResponse<TextureListResponse>> {
  const query = new URLSearchParams();
  if (params.type) query.append('type', params.type);
  if (params.order) query.append('order', params.order);
  if (params.tag) query.append('tag', params.tag);
  if (params.page) query.append('page', params.page.toString());

  const url = `${SkinlibUrl}/texture/listpreview${query.toString() ? `?${query.toString()}` : ''}`;

  return request<TextureListResponse>(url, {
    method: 'GET',
  });
}

export async function pullTexture(hash: string): Promise<Blob | { success: false; message: string }> {
  try {
    const url = `${SkinlibUrl}/texture/pull/${hash}`;

    const resp = await fetch(url, {
      method: 'GET',
    });

    if (!resp.ok) {
      const body = await resp.json().catch(() => null);
      return {
        success: false,
        message: body?.message || `拉取材质失败 (${resp.status})`,
      };
    }

    return await resp.blob();
  } catch (error) {
    return {
      success: false,
      message: error instanceof Error ? error.message : '网络错误',
    };
  }
}

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

export function getPreviewUrl(previewFile: string): string {
  return `${SkinlibUrl}/texture/preview/${previewFile}`;
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

export async function deleteTexture(
  data: TextureDeleteRequest
): Promise<ApiResponse<TextureItem>> {
  const url = `${SkinlibUrl}/texture/delete`;
  const formData = new FormData();
  formData.append('type', data.type);
  formData.append('hash', data.hash);

  return request<TextureItem>(url, {
    method: 'POST',
    body: formData,
  });
}

export async function fetchUserTextures(): Promise<ApiResponse> {
  const url = `${BackendUrl}/texture/get`;
  return request(url, {
    method: 'POST',
  });
}
