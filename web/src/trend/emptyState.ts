import type { Trend } from '@/api/types';

/**
 * 기록이 아직 모자란 네 상태. 없으면 null이다.
 *
 * 여러 상태가 한꺼번에 참일 수 있다. 처음 온 사람에게는 "기록이 없다"가, 며칠 쌓인 사람에게는 "조금만 더"가
 * 지금 할 수 있는 일을 알려 주는 말이라서 이 순서로 고른다.
 */
export type EmptyKind = 'no_records' | 'window_empty' | 'insufficient' | 'baseline_pending' | null;

export function emptyKindOf(trend: Trend): EmptyKind {
  // conversation_days는 달력에 보이는 기간 안에서 센 값이라, 0이라고 해서 기록이 한 번도 없었다는 뜻은 아니다.
  // 평소는 기록이 쌓여야 잡히므로, 평소가 잡혀 있으면 이 기간 앞에 기록이 있다는 뜻이다. 그때는 "기록이 없다"고 말하지 않는다.
  if (trend.conversation_days === 0) {
    return trend.baseline_pending ? 'no_records' : 'window_empty';
  }
  if (trend.insufficient_records) return 'insufficient';
  if (trend.baseline_pending) return 'baseline_pending';
  return null;
}
