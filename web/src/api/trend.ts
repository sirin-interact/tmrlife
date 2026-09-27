import { api, requireBody, send } from '@/api/client';
import type { Trend } from '@/api/types';

/**
 * 최근 며칠의 점 달력. 기록이 하나도 없어도 빈 칸으로 채운 달력이 온다.
 *
 * 창의 길이, 일수, 평소와 견준 결과는 모두 서버가 담아 보낸다. 화면은 나누거나 세지 않는다.
 * 같은 값을 두 곳에서 계산하면 화면에 적힌 말과 계산이 쓴 숫자가 어긋날 수 있다.
 */
export async function fetchTrend(signal?: AbortSignal): Promise<Trend> {
  const { data, response } = await send(() => api.GET('/api/v1/trend', { signal }));
  return requireBody(data, response);
}
