import type { TrendComparison, TrendMark, TrendRowKey } from '@/api/types';

/** 추세 화면의 문구. 화면 코드에는 문구를 따로 적지 않는다. */

/** 점 달력의 표시 넷을 읽는 순서. 눈에 잘 띄는 것부터 적어서 안내가 곧 읽는 법이 되게 한다. */
export const TREND_MARK_ORDER: readonly TrendMark[] = [
  'observed',
  'not_observed',
  'not_mentioned',
  'no_conversation',
];

export const TREND_TEXT = {
  pageTitle: '변화 추세 · 내일',
  title: '변화 추세',
  lead: '대화에서 읽어 낸 마음 신호를 날마다 점으로 찍어 뒀어요.',
  asOf: (date: string) => `${date}까지의 기록이에요.`,
  asOfNote: '오늘 나눈 이야기는 정리가 끝나면 이 달력에 나타나요.',
  // 점수와 단계를 보여 주지 않는 화면이라, 이 점들이 무엇인지 먼저 밝혀 둔다.
  notATest: '대화에서 읽어 낸 이야기예요. 검사나 진단의 결과가 아니에요.',

  calendarLabel: '날마다의 마음 신호',
  // 빈 칸이 셈과 어긋나 보이지 않게 "점은 이야기한 날에만 찍힌다"를 여기서 미리 말해 둔다.
  calendarCaption:
    '가로는 날짜, 세로는 기분·수면·에너지예요. 점은 이야기한 날에만 찍혀요. 점이나 날짜를 누르면 그날의 기록을 볼 수 있어요.',
  dayColumn: '날짜',

  rowLabel: { mood: '기분', sleep: '수면', energy: '에너지' } satisfies Record<TrendRowKey, string>,
  rowNote: {
    mood: '마음이 가라앉거나 하고 싶은 일이 줄었다는 이야기',
    sleep: '잠들기 어렵거나 너무 많이 잤다는 이야기',
    energy: '기운이 없거나 쉽게 지쳤다는 이야기',
  } satisfies Record<TrendRowKey, string>,

  mark: {
    observed: '신호가 보인 날',
    not_observed: '이야기가 나왔고 괜찮았던 날',
    not_mentioned: '그 이야기는 없었던 날',
    no_conversation: '대화하지 않은 날',
  } satisfies Record<TrendMark, string>,
  legendTitle: '점 읽는 법',

  /**
   * "이야기한 11일 가운데 7일이에요." 서버가 센 일수를 그대로 쓴다. 화면은 나누거나 세지 않는다.
   *
   * 분모가 무엇인지 문장에 적는다. 달력에 칸이 열넷인데 "1일 중 0일이에요"라고만 적으면 셈이 틀린 것으로 읽힌다.
   * 분모는 달력의 칸 수가 아니라 그 줄의 이야기가 나온 날의 수다.
   */
  windowCount: (days: number, observedDays: number) =>
    `이야기한 ${days}일 가운데 ${observedDays}일이에요.`,
  usualCount: (days: number, observedDays: number) =>
    `평소에는 이야기한 ${days}일 가운데 ${observedDays}일이었어요.`,
  /** 평소와 견준 결과. 견줄 수 없으면 아무 말도 붙이지 않는다. */
  comparison: {
    more_often: '평소보다 잦아요.',
    similar: '평소와 비슷해요.',
    less_often: '평소보다 드물어요.',
    none: '',
  } satisfies Record<TrendComparison, string>,

  noRecordsTitle: '아직 기록이 없어요',
  noRecordsBody:
    '오늘 이야기를 나누면 이 달력에 첫 점이 찍혀요. 며칠 쌓이면 무엇이 달라졌는지 함께 볼 수 있어요.',
  startTalking: '오늘 이야기하기',

  /** 앞선 기록은 있지만 이 달력의 기간에는 이야기한 날이 없는 사람. "기록이 없다"는 사실이 아니다. */
  windowEmptyTitle: '이 기간에는 기록이 없어요',
  windowEmptyBody:
    '달력에 보이는 날에는 나눈 이야기가 없어요. 그 앞의 기록은 그대로 있고, 오늘 이야기를 나누면 이 달력에도 다시 점이 찍혀요.',

  insufficientTitle: '조금만 더 모아요',
  insufficientBody: '이야기한 날이 아직 적어서 평소와 견주지 않았어요. 점 달력만 먼저 보여 드려요.',
  /** 창 안에서 센 일수다. 지금까지의 합이 아니라서 "지금까지"라고 적지 않는다. */
  insufficientCount: (windowDays: number, days: number) =>
    `최근 ${windowDays}일 가운데 ${days}일 이야기했어요.`,

  baselinePendingTitle: '평소를 찾고 있어요',
  baselinePendingBody:
    '처음 며칠의 기록을 그 사람의 평소로 삼아요. 그 기간이 지나면 최근과 평소를 견줘 드릴게요.',

  loading: '추세를 불러오고 있어요.',
  retry: '다시 불러오기',
} as const;
