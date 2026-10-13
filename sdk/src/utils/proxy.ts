// This utility acts as a bridge between the SDK and the HRPAuth-Web host application.
// When the SDK is aggregated, these relative imports will point to the host's actual utilities.

// @ts-ignore
export { BackendUrl, getRealBackendUrl } from '../../../../../utils/config';
// @ts-ignore
export { request } from '../../../../../utils/api';
// @ts-ignore
export type { ApiResponse } from '../../../../../utils/api';
// @ts-ignore
export { getAuthToken, getUid, getMbeEnabled, setMbeEnabled } from '../../../../../utils/cookie';
// @ts-ignore
export { dataCache } from '../../../../../utils/dataCache';

const SERVICE_PREFIX = '/yggdrasil-api';

export function getServiceBackendUrl(baseUrl: string): string {
  const base = (baseUrl || '').replace(/\/$/, '');
  return `${base}${SERVICE_PREFIX}`;
}

export function buildServiceUrl(baseUrl: string, path: string): string {
  if (!path) {
    return getServiceBackendUrl(baseUrl);
  }
  const cleanPath = path.startsWith('/') ? path : `/${path}`;
  return `${getServiceBackendUrl(baseUrl)}${cleanPath}`;
}
