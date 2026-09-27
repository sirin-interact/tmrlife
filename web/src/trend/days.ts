/** 점 달력의 가로축에 쓰는 짧은 날짜 표기. 기록 날짜는 시각도 시간대도 없으므로 UTC로만 읽는다. */

/** "2026-09-27" → 27. 열이 좁아서 날만 적는다. 달과 요일은 화면 낭독기용 글에 담는다. */
export function dayOfMonth(recordDate: string): number {
  return Number(recordDate.slice(8, 10));
}

/**
 * 달력에 든 날의 수(양쪽 끝을 포함한다). 서버가 보낸 첫날과 마지막 날로 센다.
 *
 * 이야기한 날의 수를 말할 때 그 값이 며칠을 두고 센 것인지 함께 적으려고 쓴다.
 * 날의 수를 세는 일은 계산 규칙이 아니라 같은 값을 다른 꼴로 적는 일이다.
 */
export function windowLength(from: string, to: string): number {
  const start = Date.parse(`${from}T00:00:00.000Z`);
  const end = Date.parse(`${to}T00:00:00.000Z`);
  if (Number.isNaN(start) || Number.isNaN(end) || end < start) return 0;
  return Math.round((end - start) / 86_400_000) + 1;
}

/** 주말인지. 열 머리의 진하기를 살짝 달리해 두 주가 어디서 끊기는지 눈에 들어오게 한다. */
export function isWeekend(recordDate: string): boolean {
  const day = new Date(`${recordDate}T00:00:00.000Z`).getUTCDay();
  return day === 0 || day === 6;
}
