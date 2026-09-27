import type { QueryClient } from '@tanstack/react-query';

import type { RecordDate } from '@/api/types';

/**
 * 마음 신호를 읽는 세 화면(추세, 근거, 계산 살펴보기)의 조회 키.
 *
 * 한곳에 모아 두는 까닭: 세 화면은 같은 기록을 서로 다르게 보여 준다. 신호 하나를 빼거나 하루를 지우면
 * 셋 다 낡는다. 키가 화면마다 흩어져 있으면 기록을 고치는 쪽이 어느 화면을 낡은 것으로 표시해야 하는지 알 수 없어서,
 * "이건 아니에요"를 누른 직후에 추세 화면이 아직 찬 점을 보여 주는 일이 생긴다.
 */
export const signalsKeyPrefix = ['signals'] as const;

export const daySignalsKey = (date: RecordDate) => [...signalsKeyPrefix, 'day', date] as const;

export const trendQueryKey = ['trend'] as const;

export const internalReviewQueryKey = ['internal-review'] as const;

/**
 * 하루의 신호가 바뀐 뒤에 부른다. 여러 날을 함께 보는 화면만 낡은 것으로 표시한다.
 *
 * 하루의 조회는 건드리지 않는다. 취소와 되돌림의 응답이 그 하루를 다시 합친 모습이라,
 * 방금 받은 답을 낡은 것으로 표시하면 같은 것을 곧바로 다시 물어보게 된다.
 */
export function invalidateSignalAggregates(client: QueryClient): void {
  void client.invalidateQueries({ queryKey: trendQueryKey });
  void client.invalidateQueries({ queryKey: internalReviewQueryKey });
}

/** 하루를 통째로 지운 뒤에 부른다. 그 하루의 신호까지 함께 낡은 것으로 표시한다. */
export function invalidateSignalViews(client: QueryClient): void {
  void client.invalidateQueries({ queryKey: signalsKeyPrefix });
  invalidateSignalAggregates(client);
}
