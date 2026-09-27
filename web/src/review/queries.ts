import { queryOptions } from '@tanstack/react-query';

import { fetchInternalReview } from '@/api/review';
import { internalReviewQueryKey } from '@/signals/keys';

export { internalReviewQueryKey };

/**
 * 내부 확인 화면이 읽는 조회.
 *
 * 볼 수 있는 계정인지는 서버가 판단한다. 화면이 먼저 걸러 내지 않는 까닭: 로그인 응답에는 시연 계정 여부만 있고
 * 관리자 여부가 없어서, 화면이 미리 막으면 관리자가 볼 수 있는 화면을 볼 수 없게 된다.
 */
export const internalReviewQueryOptions = queryOptions({
  queryKey: internalReviewQueryKey,
  queryFn: ({ signal }) => fetchInternalReview(signal),
});
