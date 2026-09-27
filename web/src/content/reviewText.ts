import type {
  ConfidenceComponent,
  ConfidenceLevel,
  ScoreBand,
  SignalItem,
  StageReason,
  TrendRowKey,
} from '@/api/types';
import { eul } from '@/lib/josa';

/**
 * 내부 확인 화면의 문구.
 *
 * 만드는 사람과 살펴보는 사람이 "왜 이 값인가"를 눈으로 따라가는 화면이다. 그래서 중간값을 줄이지 않고 다 적는다.
 * 여기 있는 숫자는 사용자 화면에 나가지 않는다.
 */

export const ITEM_LABEL = {
  interest: '흥미 저하',
  // 옆 항목('흥미 저하')과 같은 꼴로 적는다. 추세 줄의 '기분'은 여러 항목을 묶은 이름이라 여기서는 쓰지 않는다.
  mood: '기분 저하',
  sleep: '수면',
  fatigue: '피로',
  appetite: '식욕',
  self_blame: '자책',
  concentration: '집중',
  psychomotor: '행동 속도',
} satisfies Record<SignalItem, string>;

export const TREND_ROW_LABEL = {
  mood: '기분',
  sleep: '수면',
  energy: '에너지',
} satisfies Record<TrendRowKey, string>;

export const BAND_LABEL = {
  none: '구하지 않음',
  minimal: '아주 낮음',
  mild: '가벼움',
  moderate: '중간',
  moderately_severe: '다소 높음',
  severe: '높음',
} satisfies Record<ScoreBand, string>;

export const CONFIDENCE_LEVEL_LABEL = {
  low: '낮음',
  medium: '보통',
  high: '높음',
} satisfies Record<ConfidenceLevel, string>;

export const CONFIDENCE_COMPONENT_LABEL = {
  none: '없음',
  record_coverage: '기록 충실도',
  item_coverage: '항목 충족도',
  explicitness: '근거 명시성',
} satisfies Record<ConfidenceComponent, string>;

/**
 * 최종 값을 정한 요소를 말하는 문장.
 *
 * 이름에 조사를 이어 붙이면 '항목 충족도이에요'처럼 어색해진다. 요소가 셋뿐이라 문장째로 적어 둔다.
 */
export const CONFIDENCE_LIMITING_SENTENCE = {
  none: '',
  record_coverage: '가장 작은 요소는 기록 충실도예요.',
  item_coverage: '가장 작은 요소는 항목 충족도예요.',
  explicitness: '가장 작은 요소는 근거 명시성이에요.',
} satisfies Record<ConfidenceComponent, string>;

export const CONFIDENCE_COMPONENT_NOTE = {
  none: '',
  record_coverage: '창 안에서 이야기한 날 ÷ 창의 길이',
  item_coverage: '이야기가 나온 항목 수 ÷ 여덟',
  explicitness: '직접 말한 것에 근거한 판단 수 ÷ 관찰됨 판단 수',
} satisfies Record<ConfidenceComponent, string>;

/** 개입 단계의 이름. 0부터 3까지. */
export const STAGE_LABEL: readonly string[] = ['일상', '회고', '제안', '권유'];

export const STAGE_REASON_LABEL = {
  score: '점수가 올렸다',
  change_detected: '변화 감지로 1단계',
  sustained: '2단계 이상이 이어져 3단계',
  held_no_record_today: '그날 기록이 없어 오르지 못했다',
  held_one_step_per_day: '하루에 한 단계만 올랐다',
  held_low_confidence: '신뢰도가 낮아 오르지 못했다',
  held_insufficient_records: '기록 부족으로 오르지 못했다',
  carried_insufficient_records: '기록 부족으로 전날의 단계를 이어 갔다',
  no_recent_records: '최근 기록이 없어 0단계로 돌아갔다',
} satisfies Record<StageReason, string>;

export const REVIEW_TEXT = {
  pageTitle: '계산 살펴보기 · 내일',
  title: '계산 살펴보기',
  /** 화면에서 제일 먼저 읽혀야 하는 말이다. 이 숫자가 무엇이 아닌지를 먼저 밝힌다. */
  estimateNotice:
    '이 화면의 숫자는 대화 기록에서 추정한 값이에요. 정식 자가 점검이나 검사의 결과가 아니고, 진단도 아니에요.',
  audienceNotice:
    '만드는 사람과 살펴보는 사람을 위한 화면이에요. 점수와 개입 단계는 사용자 화면에 숫자로 보여 주지 않아요.',
  asOf: (date: string) => `기준일 ${date}`,
  asOfNote: '오늘의 분석이 아직 없으면 어제가 기준일이에요.',

  loading: '계산을 불러오고 있어요.',
  retry: '다시 불러오기',
  forbiddenTitle: '시연 계정에서만 볼 수 있어요',
  forbiddenBody:
    '이 화면은 시연 계정과 관리자 계정에만 열려 있어요. 지금 로그인한 계정으로는 볼 수 없어요.',
  forbiddenWhy:
    '추정 점수와 개입 단계를 숫자로 보여 주지 않는 까닭이에요. 힘든 사람에게 점수와 경고는 도움보다 상처가 되기 쉽고, 이 값은 검사 결과가 아니라 대화 기록에서 추정한 값이에요.',
  backHome: '처음 화면으로',

  extractorsTitle: '신호를 남긴 추출기',
  extractorsNote:
    '신호 행마다 함께 저장한 표시예요. 지시문의 판과 실제로 답한 모델을 담습니다. 어디서 나온 판단인지를 세는 것이라 취소한 행도 함께 셉니다.',
  extractorsNone: '아직 신호 행이 없어요.',
  extractorsColumns: {
    version: '표시',
    rows: '행',
    lastAt: '마지막 기록',
  },
  extractorsScriptedBadge: '모의 분석',
  /** 이 경고가 화면에 없으면 낱말 표로 채운 행과 모델이 읽은 행을 가릴 수 없다. */
  extractorsScriptedWarning: (rows: number) =>
    `정해 둔 답으로 채운 행이 ${rows}개 있어요. 낱말 표로 여덟 항목을 채운 것이고, 모델이 읽은 판단이 아니에요.`,
  extractorsAllReal: '모두 모델이 읽은 판단이에요.',

  scoreTitle: '추정 점수',
  scoreWindowLabel: '창',
  scoreInsufficient: '기록 부족이라 점수를 구하지 않았어요',
  scoreInsufficientNote: (min: number) =>
    `창 안에서 이야기한 날이 ${min}일보다 적으면 점수를 내지 않아요. 0점("충분히 듣고 신호가 없었다")과는 다른 상태예요.`,
  scoreTotal: '합계',
  scoreTotalOf: (total: number, max: number) => `${total} / ${max}`,
  scoreBandBounds: (bounds: readonly number[]) =>
    `구간 경계 ${bounds.join(' · ')}점 (가벼움 · 중간 · 다소 높음 · 높음)`,
  scoreItemBounds: (bounds: readonly number[]) =>
    `환산 일수가 ${bounds.join(' · ')}일 이상이면 항목 점수 1 · 2 · 3점이에요.`,
  scoreItemsLabel: '항목별 점수',
  scoreColumns: {
    item: '항목',
    observedDays: '관찰된 날',
    convertedDays: '환산 일수',
    points: '점수',
  },
  scoreChartLabel: '항목별 점수 0~3점',

  confidenceTitle: '신뢰도',
  confidenceLevel: (level: string) => `신뢰도 ${level}`,
  confidenceLimitingNone: '최종 값을 구하지 않아 가리킬 요소가 없어요.',
  confidenceMin:
    '최종 값은 세 요소의 평균이 아니라 가장 작은 값이에요. 평균을 내면 한 요소가 아주 나빠도 다른 요소에 묻혀요.',
  confidenceBounds: (medium: number, high: number) =>
    `${medium} 미만이면 낮음, ${high} 이상이면 높음, 그 사이는 보통이에요.`,
  confidenceInsufficient: '기록 부족이라 최종 값을 구하지 않았어요',
  confidenceConversationDays: '이야기한 날',
  confidenceMentionedItems: '이야기가 나온 항목',
  confidenceMissingItems: '한 번도 나오지 않은 항목',
  confidenceMissingNone: '없어요',
  confidenceObservedJudgements: '관찰됨 판단',
  confidenceDirectJudgements: '그 가운데 직접 말한 것',

  baselineTitle: '평소',
  baselineEstablished: '평소가 잡혔어요',
  baselinePending: '평소가 아직 잡히지 않았어요',
  baselinePendingNote: '잡히기 전의 값은 "지금까지 모인 값"이라 평소로 쓰면 안 돼요.',
  baselineExtended: '첫 기간에 이야기한 날이 모자라서 기간을 늘렸어요.',
  baselineWindow: '기간',
  baselineWindowOpen: (start: string) => `${start} ~ (아직 정할 수 없음)`,
  baselineNoStart: '이야기한 날이 없어요',
  baselineDays: '평소를 정하는 데 쓴 날',
  baselineObservedTotal: '그 날들의 관찰된 항목 수 합계',
  baselineMu: '하루 평균 신호 수',
  baselineMuFormula: (total: number, days: number) => `${total} ÷ ${days}`,
  baselineParams: (windowDays: number, minDays: number) =>
    `첫 대화 날부터 ${windowDays}일을 평소로 삼고, 그 안에 이야기한 날이 ${minDays}일보다 적으면 찰 때까지 기간을 늘려요.`,
  baselineItemRates: '항목별 관찰 비율',
  baselineTrendRates: '추세 줄별 관찰 비율',
  rateOf: (days: number, observedDays: number) => `${days}일 중 ${observedDays}일`,

  changeTitle: '변화 탐지',
  changeNotRunning: '평소가 잡히기 전이라 아직 돌지 않아요',
  changeDetected: '변화 감지 상태예요',
  changeNotDetected: '변화 감지 상태가 아니에요',
  changeS: '기준일까지의 누적값',
  changeFrom: (date: string) => `${date}부터 쌓았어요.`,
  changeParams: (k: number, h: number) =>
    `평소보다 ${k}만큼 많은 것까지는 흔한 기복으로 보고 쌓지 않고, 누적값이 ${h}${eul(h)} 넘으면 변화 감지예요.`,
  changeCeiling: (maxStep: number, maxS: number) =>
    `하루에 늘 수 있는 양은 ${maxStep === 0 ? '제한 없음' : maxStep}이고, 누적값의 천장은 한계값의 ${maxS === 0 ? '없음' : `${maxS}배`}예요.`,
  changeChartLabel: '날마다의 누적값',
  changeThreshold: (h: number) => `한계값 ${h}`,
  changeDetectionOn: '감지 시작',
  changeDetectionOff: '풀림',
  changeEmpty: '아직 쌓은 날이 없어요.',
  changeColumns: {
    date: '날짜',
    observed: '관찰된 항목',
    step: '더한 값',
    s: '누적값',
    detected: '감지',
  },
  changeCapped: '상한',
  changeAtCeiling: '천장',

  stageTitle: '개입 단계',
  stageNow: (stage: number, name: string) => `${stage}단계 · ${name}`,
  stageFrom: (date: string) => `${date}부터 하루씩 다시 돌린 흐름이에요.`,
  stageNoSeries: '아직 이야기한 날이 없어요.',
  stageParams: (bounds: readonly number[], sustained: number) =>
    `추정 점수가 ${bounds.join(' · ')}점 이상이면 1 · 2 · 3단계이고, 2단계 이상이 ${sustained}일째 이어지면 3단계로 올려요.`,
  stageRules:
    '단계는 이야기한 날에만 오르고, 하루에 한 단계만 오르고, 신뢰도가 낮은 날에는 오르지 않아요.',
  stageChartLabel: '날마다의 개입 단계',
  stageHeld: '오르지 못한 날',
  stageColumns: {
    date: '날짜',
    record: '기록',
    score: '점수',
    confidence: '신뢰도',
    detected: '감지',
    raw: '그날의 값',
    stage: '단계',
    reasons: '움직인 조건',
  },
  stageHasRecord: '있음',
  stageNoRecord: '없음',
  stageElevatedDays: (days: number) => `${days}일째`,

  paramsTitle: '조정 값',
  paramsNote:
    '이 계산에 쓴 값이에요. 화면이 경계를 코드에 박아 두지 않고 서버가 보낸 값을 그대로 씁니다.',
  paramsLabel: {
    windowDays: '창의 길이',
    minConversationDays: '점수를 내는 데 필요한 이야기한 날',
    itemScoreMinDays: '항목 점수 1 · 2 · 3점의 환산 일수 경계',
    bandMinScores: '구간 경계',
    confidenceMediumMin: '신뢰도 보통의 아래쪽 경계',
    confidenceHighMin: '신뢰도 높음의 아래쪽 경계',
    baselineWindowDays: '평소로 삼는 기간',
    baselineMinConversationDays: '평소에 필요한 이야기한 날',
    cusumK: '허용 여유',
    cusumH: '한계값',
    cusumMaxStep: '하루 증가량의 상한',
    cusumMaxS: '누적값 천장(한계값의 배수)',
    stageMinScores: '1 · 2 · 3단계의 점수 경계',
    sustainedStage2Days: '3단계로 올리는 지속 일수',
    trendMinDifferencePercent: '잦음·드묾을 가르는 차이(퍼센트포인트)',
  },

  yes: '예',
  no: '아니요',
  none: '없음',
  unlimited: '없음',
  days: (days: number) => `${days}일`,
  percent: (value: number) => `${value}%p`,
} as const;
