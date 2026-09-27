import { api, requireBody, send } from '@/api/client';
import type { DaySignals, RecordDate } from '@/api/types';

/**
 * 그날 여덟 항목을 어떻게 보았는지와, 그렇게 본 근거가 된 사용자의 말.
 *
 * `analysed`가 거짓이면 `items`는 비어 있다. 대화하지 않은 날, 정리가 도는 중인 날,
 * 마음 신호 읽기를 꺼 둔 날이 모두 그렇다. 없는 날도 404가 아니라 200으로 온다.
 */
export async function fetchDaySignals(date: RecordDate, signal?: AbortSignal): Promise<DaySignals> {
  const { data, response } = await send(() =>
    api.GET('/api/v1/days/{date}/signals', { params: { path: { date } }, signal }),
  );
  return requireBody(data, response);
}

/**
 * 신호 하나를 변화 추세를 만드는 기록에서 뺀다. 행은 지우지 않는다.
 * 같은 신호에 두 번 불러도 결과가 같고, 답은 그날의 신호 전부다(항목의 합친 판단이 함께 바뀐다).
 */
export async function cancelSignal(signalId: string): Promise<DaySignals> {
  const { data, response } = await send(() =>
    api.POST('/api/v1/signals/{signalId}/cancel', { params: { path: { signalId } } }),
  );
  return requireBody(data, response);
}

/** 빼 두었던 신호를 다시 기록에 넣는다. 뺀 적이 없는 신호에 불러도 성공한다. */
export async function uncancelSignal(signalId: string): Promise<DaySignals> {
  const { data, response } = await send(() =>
    api.POST('/api/v1/signals/{signalId}/uncancel', { params: { path: { signalId } } }),
  );
  return requireBody(data, response);
}
