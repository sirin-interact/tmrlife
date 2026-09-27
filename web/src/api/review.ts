import { api, requireBody, send } from '@/api/client';
import type { InternalReview } from '@/api/types';

/**
 * 계산이 그 값에 이른 과정 전부. 시연 계정과 관리자 계정만 읽을 수 있고, 그 밖에는 403 forbidden이다.
 * 거절은 실패가 아니라 답이므로 화면이 조용히 안내한다.
 */
export async function fetchInternalReview(signal?: AbortSignal): Promise<InternalReview> {
  const { data, response } = await send(() => api.GET('/api/v1/internal/review', { signal }));
  return requireBody(data, response);
}
