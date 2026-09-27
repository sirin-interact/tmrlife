import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';

import type { InternalReview, SignalItem } from '@/api/types';
import { json, mockApi, networkFailure, problem, signedIn } from '@/test/mockApi';
import { renderRoute } from '@/test/renderRoute';

const ITEMS: readonly SignalItem[] = [
  'interest',
  'mood',
  'sleep',
  'fatigue',
  'appetite',
  'self_blame',
  'concentration',
  'psychomotor',
];

/** 시연에 쓸 만한 상태. 점수도, 평소도, 변화 감지도, 오르지 못한 단계도 모두 들어 있다. */
const review: InternalReview = {
  as_of: '2026-09-27',
  extractors: [
    { version: '82d709f9b439/gemini-3.8-flash', rows: 176, last_at: '2026-09-27T11:20:00Z' },
  ],
  params: {
    window_days: 14,
    min_conversation_days: 7,
    item_score_min_days: [4, 7, 11],
    band_min_scores: [5, 10, 15, 20],
    confidence_medium_min: 0.5,
    confidence_high_min: 0.75,
    baseline_window_days: 14,
    baseline_min_conversation_days: 7,
    cusum_k: 0.5,
    cusum_h: 4,
    cusum_max_step: 3,
    cusum_max_s: 3,
    stage_min_scores: [5, 10, 15],
    sustained_stage2_days: 14,
    trend_min_difference_percent: 15,
  },
  score: {
    window_from: '2026-09-14',
    window_to: '2026-09-27',
    conversation_days: 11,
    insufficient: false,
    items: [
      { item: 'interest', observed_days: 7, converted_days: 8.9, points: 2 },
      { item: 'mood', observed_days: 9, converted_days: 11.5, points: 3 },
      { item: 'sleep', observed_days: 2, converted_days: 2.5, points: 0 },
      { item: 'fatigue', observed_days: 5, converted_days: 6.4, points: 1 },
      { item: 'appetite', observed_days: 0, converted_days: 0, points: 0 },
      { item: 'self_blame', observed_days: 3, converted_days: 3.8, points: 0 },
      { item: 'concentration', observed_days: 4, converted_days: 5.1, points: 1 },
      { item: 'psychomotor', observed_days: 1, converted_days: 1.3, points: 0 },
    ],
    total: 7,
    band: 'mild',
  },
  confidence: {
    conversation_days: 11,
    mentioned_items: 7,
    missing_items: ['appetite'],
    observed_judgements: 31,
    direct_judgements: 24,
    record_coverage: { num: 11, den: 14 },
    item_coverage: { num: 7, den: 8 },
    explicitness: { num: 24, den: 31 },
    insufficient: false,
    // 세 요소 가운데 가장 작은 값(24/31)이 최종 값이다. 평균이 아니다.
    value: { num: 24, den: 31 },
    limiting: 'explicitness',
    level: 'high',
  },
  baseline: {
    established: true,
    start: '2026-08-20',
    end: '2026-09-02',
    extended: false,
    days: 9,
    observed_total: 18,
    mu: 2,
    item_rates: ITEMS.map((item, index) => ({
      item,
      rate: { observed_days: index, days: 9 },
    })),
    trend_rates: [
      { row: 'mood', rate: { observed_days: 5, days: 9 } },
      { row: 'sleep', rate: { observed_days: 3, days: 9 } },
      { row: 'energy', rate: { observed_days: 2, days: 9 } },
    ],
  },
  change: {
    running: true,
    detected: true,
    s: 5.5,
    from: '2026-09-03',
    series: [
      {
        date: '2026-09-17',
        observed: 1,
        step: -1.5,
        capped: false,
        at_ceiling: false,
        s: 0,
        detected: false,
      },
      {
        date: '2026-09-18',
        observed: 3,
        step: 0.5,
        capped: false,
        at_ceiling: false,
        s: 0.5,
        detected: false,
      },
      {
        date: '2026-09-20',
        observed: 5,
        step: 2.5,
        capped: false,
        at_ceiling: false,
        s: 3,
        detected: false,
      },
      {
        date: '2026-09-21',
        observed: 6,
        step: 3,
        capped: true,
        at_ceiling: false,
        s: 6,
        detected: true,
      },
      {
        date: '2026-09-23',
        observed: 2,
        step: -0.5,
        capped: false,
        at_ceiling: false,
        s: 5.5,
        detected: true,
      },
      {
        date: '2026-09-25',
        observed: 1,
        step: -1.5,
        capped: false,
        at_ceiling: false,
        s: 3.5,
        detected: false,
      },
      {
        date: '2026-09-27',
        observed: 5,
        step: 2.5,
        capped: false,
        at_ceiling: true,
        s: 5.5,
        detected: true,
      },
    ],
  },
  stage: {
    stage: 2,
    from: '2026-09-22',
    series: [
      {
        date: '2026-09-22',
        has_record: true,
        conversation_days: 6,
        insufficient: true,
        score: 0,
        confidence: 'low',
        detected: false,
        raw: 0,
        stage: 0,
        held: false,
        elevated_days: 0,
        reasons: ['held_insufficient_records'],
      },
      {
        date: '2026-09-23',
        has_record: false,
        conversation_days: 6,
        insufficient: true,
        score: 0,
        confidence: 'low',
        detected: false,
        raw: 0,
        stage: 0,
        held: false,
        elevated_days: 0,
        reasons: ['carried_insufficient_records'],
      },
      {
        date: '2026-09-24',
        has_record: true,
        conversation_days: 8,
        insufficient: false,
        score: 7,
        confidence: 'medium',
        detected: false,
        raw: 1,
        stage: 1,
        held: false,
        elevated_days: 0,
        reasons: ['score'],
      },
      {
        date: '2026-09-25',
        has_record: true,
        conversation_days: 9,
        insufficient: false,
        score: 12,
        confidence: 'medium',
        detected: false,
        raw: 2,
        stage: 2,
        held: false,
        elevated_days: 1,
        reasons: ['score'],
      },
      {
        date: '2026-09-26',
        has_record: true,
        conversation_days: 10,
        insufficient: false,
        score: 16,
        confidence: 'low',
        detected: false,
        raw: 3,
        stage: 2,
        held: true,
        elevated_days: 2,
        reasons: ['held_low_confidence'],
      },
      {
        date: '2026-09-27',
        has_record: false,
        conversation_days: 11,
        insufficient: false,
        score: 16,
        confidence: 'medium',
        detected: true,
        raw: 3,
        stage: 2,
        held: true,
        elevated_days: 3,
        reasons: ['held_no_record_today'],
      },
    ],
  },
};

/** 아직 기록이 모자란 계정. 서버는 점수와 신뢰도의 최종 값을 구하지 않는다. */
const insufficient: InternalReview = {
  ...review,
  score: {
    ...review.score,
    conversation_days: 3,
    insufficient: true,
    items: review.score.items.map((item) => ({ ...item, converted_days: 0, points: 0 })),
    total: 0,
    band: 'none',
  },
  confidence: {
    ...review.confidence,
    insufficient: true,
    value: { num: 0, den: 0 },
    limiting: 'none',
    level: 'low',
  },
};

function serveReview(body: InternalReview) {
  return mockApi({
    'GET /api/v1/me': signedIn,
    'GET /api/v1/internal/review': () => json(200, body),
  });
}

describe('내부 확인 화면: 값이 어떻게 나왔는지', () => {
  it('추정 점수와 항목별 중간값을 모두 보여 준다', async () => {
    serveReview(review);

    renderRoute('/internal/review');

    const table = await screen.findByRole('table', { name: '항목별 점수' });
    const mood = within(table).getByRole('rowheader', { name: '기분 저하' }).closest('tr');
    expect(
      within(mood as HTMLElement)
        .getAllByRole('cell')
        .map((cell) => cell.textContent),
    ).toEqual(['9', '11.5', '3']);
    // 합계와 구간, 그리고 그 경계를 함께 적어 두어 왜 이 구간인지 눈으로 따라갈 수 있다.
    const score = screen.getByRole('region', { name: '추정 점수' });
    expect(within(score).getByText('/ 24 · 가벼움')).toBeInTheDocument();
    expect(within(score).getByText('7 / 24')).toBeInTheDocument();
    expect(
      screen.getByText('구간 경계 5 · 10 · 15 · 20점 (가벼움 · 중간 · 다소 높음 · 높음)'),
    ).toBeInTheDocument();
    expect(
      screen.getByText('환산 일수가 4 · 7 · 11일 이상이면 항목 점수 1 · 2 · 3점이에요.'),
    ).toBeInTheDocument();
    expect(document.title).toBe('계산 살펴보기 · 내일');
  });

  it('신뢰도의 세 요소와 최종 값을 정한 요소를 보여 준다', async () => {
    serveReview(review);

    renderRoute('/internal/review');

    expect(await screen.findByText('신뢰도 높음')).toBeInTheDocument();
    // 최종 값이 요소 하나의 값과 같으므로 머리줄과 그 요소에 같은 분수가 적힌다.
    expect(screen.getAllByText('24 / 31')).toHaveLength(2);
    expect(screen.getByText('가장 작은 요소는 근거 명시성이에요.')).toBeInTheDocument();
    const confidence = screen.getByRole('region', { name: '신뢰도' });
    for (const component of ['기록 충실도', '항목 충족도', '근거 명시성']) {
      expect(within(confidence).getByText(component)).toBeInTheDocument();
    }
    expect(within(confidence).getByText('11 / 14')).toBeInTheDocument();
    expect(within(confidence).getByText('7 / 8')).toBeInTheDocument();
    // 판정 기준은 소수(0.5 · 0.75)다. 분수만 적어 두면 "신뢰도 높음"이 맞는지 보는 사람이 나눠 봐야 한다.
    expect(
      within(confidence).getByText('0.5 미만이면 낮음, 0.75 이상이면 높음, 그 사이는 보통이에요.'),
    ).toBeInTheDocument();
    expect(within(confidence).getAllByText('(0.77)')).toHaveLength(2);
    expect(within(confidence).getByText('(0.79)')).toBeInTheDocument();
    expect(within(confidence).getByText('(0.88)')).toBeInTheDocument();
    // 최종 값을 정한 요소를 색만으로 가리지 않고 글로 짚어 둔다.
    expect(within(confidence).getByText('가장 작음')).toBeInTheDocument();
    // 한 번도 나오지 않은 항목은 다음 대화에서 물어볼 항목을 고르는 데 쓴다.
    expect(within(confidence).getByText('식욕')).toBeInTheDocument();
  });

  it('평소의 기간과 하루 평균을 분자와 분모까지 보여 준다', async () => {
    serveReview(review);

    renderRoute('/internal/review');

    expect(await screen.findByText('평소가 잡혔어요')).toBeInTheDocument();
    expect(screen.getByText('8월 20일 목요일 ~ 9월 2일 수요일')).toBeInTheDocument();
    expect(screen.getByText('(18 ÷ 9)')).toBeInTheDocument();
  });

  it('누적값의 흐름과 감지가 켜지고 풀린 날을 보여 준다', async () => {
    serveReview(review);

    renderRoute('/internal/review');

    expect(await screen.findByRole('img', { name: '날마다의 누적값' })).toBeInTheDocument();
    expect(screen.getAllByText('감지 시작')).toHaveLength(2);
    expect(screen.getByText('풀림')).toBeInTheDocument();
    expect(screen.getByText('한계값 4')).toBeInTheDocument();
    expect(screen.getByText('변화 감지 상태예요')).toBeInTheDocument();
    // 4는 "사"로 읽으므로 "4를"이다. 숫자에 맞는 조사를 고른다.
    expect(
      screen.getByText(
        '평소보다 0.5만큼 많은 것까지는 흔한 기복으로 보고 쌓지 않고, 누적값이 4를 넘으면 변화 감지예요.',
      ),
    ).toBeInTheDocument();
    // 하루 증가량이 상한에 걸린 날과 누적값이 천장에 걸린 날을 표에 적어 둔다.
    expect(screen.getByText('상한')).toBeInTheDocument();
    expect(screen.getByText('천장')).toBeInTheDocument();
  });

  it('개입 단계의 흐름과 오르지 못한 까닭을 보여 준다', async () => {
    serveReview(review);

    renderRoute('/internal/review');

    expect(await screen.findByText('2단계 · 제안')).toBeInTheDocument();
    expect(screen.getByRole('img', { name: '날마다의 개입 단계' })).toBeInTheDocument();
    expect(screen.getByText('오르지 못한 날 2일')).toBeInTheDocument();
    expect(screen.getByText('신뢰도가 낮아 오르지 못했다')).toBeInTheDocument();
    expect(screen.getByText('그날 기록이 없어 오르지 못했다')).toBeInTheDocument();
    expect(screen.getByText('기록 부족으로 전날의 단계를 이어 갔다')).toBeInTheDocument();
  });

  it('신호 행을 남긴 추출기의 표시와 행 수를 보여 준다', async () => {
    serveReview(review);

    renderRoute('/internal/review');

    expect(await screen.findByText('82d709f9b439/gemini-3.8-flash')).toBeInTheDocument();
    const table = screen.getByRole('table', { name: '신호를 남긴 추출기' });
    expect(within(table).getByText('176')).toBeInTheDocument();
    expect(screen.getByText('모두 모델이 읽은 판단이에요.')).toBeInTheDocument();
    expect(screen.queryByText('모의 분석')).not.toBeInTheDocument();
  });

  // 키 없이 띄운 서버는 낱말 표로 여덟 항목을 채운다. 화면에 표시가 없으면 모델이 읽은 판단과 구별할 수 없다.
  it('정해 둔 답으로 채운 행이 섞여 있으면 숫자보다 먼저 그 사실을 알린다', async () => {
    serveReview({
      ...review,
      extractors: [
        { version: '82d709f9b439/scripted', rows: 24, last_at: '2026-09-27T11:20:00Z' },
        { version: '82d709f9b439/gemini-3.8-flash', rows: 8, last_at: '2026-09-20T11:20:00Z' },
      ],
    });

    renderRoute('/internal/review');

    expect(
      await screen.findByText(
        '정해 둔 답으로 채운 행이 24개 있어요. 낱말 표로 여덟 항목을 채운 것이고, 모델이 읽은 판단이 아니에요.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByText('모의 분석')).toBeInTheDocument();
  });

  it('신호 행이 아직 없으면 빈 표 대신 그 사실을 적는다', async () => {
    serveReview({ ...review, extractors: [] });

    renderRoute('/internal/review');

    expect(await screen.findByText('아직 신호 행이 없어요.')).toBeInTheDocument();
    expect(screen.queryByRole('table', { name: '신호를 남긴 추출기' })).not.toBeInTheDocument();
  });

  it('계산에 쓴 조정 값을 그대로 적어 둔다', async () => {
    serveReview(review);

    renderRoute('/internal/review');

    expect(await screen.findByText('한계값')).toBeInTheDocument();
    expect(screen.getByText('허용 여유')).toBeInTheDocument();
    expect(screen.getByText('15%p')).toBeInTheDocument();
  });

  it('기록이 모자라면 구하지 않은 값(합계와 구간, 환산 일수)을 보여 주지 않는다', async () => {
    serveReview(insufficient);

    renderRoute('/internal/review');

    expect(await screen.findByText('기록 부족이라 점수를 구하지 않았어요')).toBeInTheDocument();
    expect(screen.getByText('기록 부족이라 최종 값을 구하지 않았어요')).toBeInTheDocument();
    const table = screen.getByRole('table', { name: '항목별 점수' });
    expect(
      within(table).queryByRole('columnheader', { name: '환산 일수' }),
    ).not.toBeInTheDocument();
    expect(within(table).queryByRole('columnheader', { name: '점수' })).not.toBeInTheDocument();
    expect(screen.queryByText('/ 24 · 구하지 않음')).not.toBeInTheDocument();
  });
});

describe('내부 확인 화면: 무엇이 아닌지', () => {
  it('추정값이고 검사 결과가 아니라는 것을 화면 맨 위에 적는다', async () => {
    serveReview(review);

    renderRoute('/internal/review');

    await screen.findByText('신뢰도 높음');
    expect(screen.getByRole('note')).toHaveTextContent(
      '이 화면의 숫자는 대화 기록에서 추정한 값이에요. 정식 자가 점검이나 검사의 결과가 아니고, 진단도 아니에요.',
    );
    expect(
      screen.getByText(/점수와 개입 단계는 사용자 화면에 숫자로 보여 주지 않아요/),
    ).toBeInTheDocument();
  });
});

describe('내부 확인 화면: 볼 수 없는 계정', () => {
  it('시연 계정이 아니면 오류가 아니라 까닭을 조용히 알린다', async () => {
    mockApi({
      'GET /api/v1/me': signedIn,
      'GET /api/v1/internal/review': () => problem(403, 'forbidden'),
    });

    renderRoute('/internal/review');

    expect(
      await screen.findByRole('heading', { level: 2, name: '시연 계정에서만 볼 수 있어요' }),
    ).toBeInTheDocument();
    // 거절은 실패가 아니다. 붉은 오류 알림으로 띄우지 않는다.
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '다시 불러오기' })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: '처음 화면으로' })).toHaveAttribute('href', '/');
    // 숫자는 하나도 나오지 않는다.
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('연결이 끊긴 것은 거절과 달리 다시 시도할 수 있게 한다', async () => {
    let online = false;
    mockApi({
      'GET /api/v1/me': signedIn,
      'GET /api/v1/internal/review': () => (online ? json(200, review) : networkFailure()),
    });

    renderRoute('/internal/review');

    expect(await screen.findByRole('alert')).toHaveTextContent('인터넷 연결이 고르지 않아요.');
    online = true;
    await userEvent.click(screen.getByRole('button', { name: '다시 불러오기' }));

    expect(await screen.findByText('신뢰도 높음')).toBeInTheDocument();
  });
});
