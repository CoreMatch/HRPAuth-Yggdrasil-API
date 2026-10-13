import { BackendUrl } from '../utils/proxy';

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
