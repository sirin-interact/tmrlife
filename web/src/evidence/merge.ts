import type { DaySignalItem, DaySignals, SignalExplicitness, SignalStatus } from '@/api/types';

/**
 * 취소한 행을 뺀 나머지로 그 항목의 하루 판단을 다시 만든다.
 *
 * 서버가 같은 규칙으로 계산해서 답에 담아 주므로, 이 값은 답이 도착하기 전까지만 쓴다.
 * 그래도 화면에서 한 번 계산하는 까닭: "이건 아니에요"를 누른 순간 그 항목의 한 줄이 그대로 남아 있으면
 * 사용자는 누른 것이 먹히지 않은 줄로 안다. 규칙은 명세에 적힌 그대로다.
 */
export function mergeItem(item: DaySignalItem): DaySignalItem {
  const kept = item.rows.filter((row) => !row.cancelled);

  // 하루에 한 번이라도 관찰됐으면 관찰됨이다. 이야기가 나온 적조차 없으면 "없었다"가 아니라 "모른다"에 가까운 언급 없음이다.
  const status: SignalStatus = kept.some((row) => row.status === 'observed')
    ? 'observed'
    : kept.some((row) => row.status === 'not_observed')
      ? 'not_observed'
      : 'not_mentioned';

  return { ...item, status, explicitness: mergeExplicitness(kept, status) };
}

// 분명한 쪽이 앞이다. 합친 판단을 받치는 행 가운데 가장 분명한 근거를 그 항목의 명시성으로 쓴다.
const ORDER: readonly SignalExplicitness[] = ['direct', 'indirect', 'none'];

function mergeExplicitness(kept: DaySignalItem['rows'], status: SignalStatus): SignalExplicitness {
  if (status === 'not_mentioned') return 'none';
  const supporting = kept.filter((row) => row.status === status);
  return ORDER.find((level) => supporting.some((row) => row.explicitness === level)) ?? 'none';
}

/**
 * 행 하나의 취소 여부를 바꾼 하루를 만든다. 원래 값은 건드리지 않는다.
 * 그 행이 속한 항목의 합친 판단도 함께 다시 계산한다. 하루가 분석된 사실(`analysed`)은 바뀌지 않는다.
 */
export function withRowCancelled(
  day: DaySignals,
  signalId: string,
  cancelled: boolean,
): DaySignals {
  return {
    ...day,
    items: day.items.map((item) =>
      item.rows.some((row) => row.id === signalId)
        ? mergeItem({
            ...item,
            rows: item.rows.map((row) => (row.id === signalId ? { ...row, cancelled } : row)),
          })
        : item,
    ),
  };
}
