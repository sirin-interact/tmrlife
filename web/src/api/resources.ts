import { api, requireBody, send } from '@/api/client';
import type { Resource } from '@/api/types';

/** 도움이 필요할 때 연락할 수 있는 곳. 급한 곳이 앞에 온다. 로그인하지 않아도 받을 수 있다. */
export async function fetchResources(signal?: AbortSignal): Promise<Resource[]> {
  const { data, response } = await send(() => api.GET('/api/v1/resources', { signal }));
  return requireBody(data, response).items;
}
