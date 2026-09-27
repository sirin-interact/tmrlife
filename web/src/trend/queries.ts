import { queryOptions } from '@tanstack/react-query';

import { fetchTrend } from '@/api/trend';
import { trendQueryKey } from '@/signals/keys';

export { trendQueryKey };

/**
 * 추세 화면이 읽는 조회.
 *
 * 응답에는 사용자가 한 말이 담기지 않는다(표시와 일수뿐이다). 그래서 다른 조회와 달리 결과를 일찍 버리지 않는다.
 * 기준일은 서버가 정하므로 날짜를 키에 넣지 않는다. 하루가 바뀌면 창에 초점이 돌아올 때 다시 받아 온다.
 */
export const trendQueryOptions = queryOptions({
  queryKey: trendQueryKey,
  queryFn: ({ signal }) => fetchTrend(signal),
});
