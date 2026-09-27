import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { Trend, TrendMark, TrendRow, TrendRowKey } from '@/api/types';
import { json, mockApi, networkFailure, signedIn } from '@/test/mockApi';
import { renderRoute } from '@/test/renderRoute';

/** 기준일이 2026-09-27인 14일 창. 앞의 사흘은 대화하지 않은 날이다. */
const DATES = [
  '2026-09-14',
  '2026-09-15',
  '2026-09-16',
  '2026-09-17',
  '2026-09-18',
  '2026-09-19',
  '2026-09-20',
  '2026-09-21',
  '2026-09-22',
  '2026-09-23',
  '2026-09-24',
  '2026-09-25',
  '2026-09-26',
  '2026-09-27',
] as const;

const N: TrendMark = 'no_conversation';
const O: TrendMark = 'observed';
const X: TrendMark = 'not_observed';
const M: TrendMark = 'not_mentioned';

const MARK_TEXT: Record<TrendMark, string> = {
  observed: '신호가 보인 날',
  not_observed: '이야기가 나왔고 괜찮았던 날',
  not_mentioned: '그 이야기는 없었던 날',
  no_conversation: '대화하지 않은 날',
};

function row(
  key: TrendRowKey,
  marks: readonly TrendMark[],
  rest: Omit<TrendRow, 'row' | 'cells'>,
): TrendRow {
  return {
    row: key,
    cells: DATES.map((date, index) => ({ date, mark: marks[index] ?? N })),
    ...rest,
  };
}

/**
 * 잘 쌓인 계정. 표시 넷이 모두 나온다.
 *
 * 일부러 칸에 찍힌 찬 점의 수(7)와 서버가 보낸 일수(9), 그리고 산술로는 "드묾"이어야 할 판단("잦음")을
 * 어긋나게 두었다. 화면이 칸을 세거나 일수를 나누어 문장을 만들면 이 어긋남에서 드러난다.
 */
const healthy: Trend = {
  as_of: '2026-09-27',
  from: '2026-09-14',
  to: '2026-09-27',
  conversation_days: 11,
  insufficient_records: false,
  baseline_pending: false,
  rows: [
    row('mood', [N, N, N, O, O, X, O, M, O, O, X, O, X, O], {
      window: { observed_days: 9, days: 12 },
      usual: { observed_days: 11, days: 12 },
      comparison: 'more_often',
    }),
    row('sleep', [N, N, N, X, X, X, M, M, X, O, O, X, X, M], {
      window: { observed_days: 2, days: 11 },
      usual: { observed_days: 6, days: 11 },
      comparison: 'less_often',
    }),
    row('energy', [N, N, N, M, M, M, M, M, M, M, M, M, M, M], {
      window: { observed_days: 0, days: 11 },
      usual: { observed_days: 0, days: 11 },
      comparison: 'similar',
    }),
  ],
};

/** 아직 한 번도 이야기하지 않은 계정. 달력은 모두 빈칸이다. */
const noRecords: Trend = {
  as_of: '2026-09-27',
  from: '2026-09-14',
  to: '2026-09-27',
  conversation_days: 0,
  insufficient_records: true,
  baseline_pending: true,
  rows: (['mood', 'sleep', 'energy'] as const).map((key) =>
    row(key, [], { window: { observed_days: 0, days: 0 }, usual: null, comparison: 'none' }),
  ),
};

/** 며칠만 이야기한 계정. 점 달력은 그리지만 평소와 견주지 않는다. */
const tooFew: Trend = {
  ...noRecords,
  conversation_days: 3,
  insufficient_records: true,
  baseline_pending: true,
  rows: [
    row('mood', [N, N, N, N, N, N, N, N, N, N, N, O, X, O], {
      window: { observed_days: 2, days: 3 },
      usual: null,
      comparison: 'none',
    }),
    row('sleep', [N, N, N, N, N, N, N, N, N, N, N, M, M, X], {
      window: { observed_days: 0, days: 3 },
      usual: null,
      comparison: 'none',
    }),
    row('energy', [N, N, N, N, N, N, N, N, N, N, N, M, M, M], {
      window: { observed_days: 0, days: 3 },
      usual: null,
      comparison: 'none',
    }),
  ],
};

/** 기록은 넉넉하지만 평소가 아직 잡히지 않은 계정. 견줄 값이 없으니 판단도 없다. */
const baselinePending: Trend = {
  ...healthy,
  baseline_pending: true,
  rows: healthy.rows.map((entry) => ({ ...entry, usual: null, comparison: 'none' as const })),
};

function serveTrend(trend: Trend) {
  return mockApi({ 'GET /api/v1/me': signedIn, 'GET /api/v1/trend': () => json(200, trend) });
}

/** 평소와 견준 판단을 말하는 문장. 안내 문구의 "평소"와 섞이지 않게 판단 그대로 찾는다. */
const COMPARISON_SENTENCE = /평소보다 잦아요|평소보다 드물어요|평소와 비슷해요|평소에는 /;

/** 기록 날짜를 고정한다. 기준일과 오늘이 같은지에 따라 머리글의 안내가 달라진다. */
function atInstant(iso: string) {
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(new Date(iso));
}

afterEach(() => {
  vi.useRealTimers();
});

async function calendar() {
  return await screen.findByRole('table', { name: '날마다의 마음 신호' });
}

/**
 * 줄 하나의 칸 열넷을 순서대로 읽는다.
 *
 * 이야기한 날의 칸은 그날로 가는 링크라서 화면 낭독기가 읽는 글이 "줄 이름 + 날짜 + 표시"로 길다.
 * 어느 표시인지만 견주려고 뒤에 붙은 표시만 골라낸다.
 */
function marksOf(table: HTMLElement, label: string): string[] {
  const header = within(table).getByRole('rowheader', { name: label });
  const tr = header.closest('tr');
  expect(tr).not.toBeNull();
  return within(tr as HTMLElement)
    .getAllByRole('cell')
    .map((cell) => {
      const text = cell.textContent ?? '';
      return Object.values(MARK_TEXT).find((mark) => text.endsWith(mark)) ?? text;
    });
}

describe('추세 화면: 점 달력', () => {
  it('날마다의 표시 넷을 모두 그리고, 대화하지 않은 날은 빈칸으로 둔다', async () => {
    serveTrend(healthy);

    renderRoute('/trend');

    const table = await calendar();
    expect(marksOf(table, '기분')).toEqual(
      [N, N, N, O, O, X, O, M, O, O, X, O, X, O].map((mark) => MARK_TEXT[mark]),
    );
    expect(marksOf(table, '수면')).toEqual(
      [N, N, N, X, X, X, M, M, X, O, O, X, X, M].map((mark) => MARK_TEXT[mark]),
    );
    expect(marksOf(table, '에너지')).toEqual(
      [N, N, N, M, M, M, M, M, M, M, M, M, M, M].map((mark) => MARK_TEXT[mark]),
    );
    expect(document.title).toBe('변화 추세 · 내일');
  });

  it('점 읽는 법을 네 가지 모두 적어 둔다', async () => {
    serveTrend(healthy);

    renderRoute('/trend');

    await calendar();
    for (const text of Object.values(MARK_TEXT)) {
      expect(screen.getAllByText(text).length).toBeGreaterThan(0);
    }
  });

  it('날짜를 누르면 그날의 근거 화면으로 가고, 대화하지 않은 날에는 누를 곳을 두지 않는다', async () => {
    serveTrend(healthy);

    renderRoute('/trend');

    const table = await calendar();
    // 이야기한 날마다 머리글 하나와 줄 셋의 칸이 모두 같은 곳으로 간다(11 × 4).
    const days = within(table).getAllByRole('link');
    expect(days).toHaveLength(44);
    expect(days[0]).toHaveAccessibleName('9월 17일 목요일');
    expect(days[0]).toHaveAttribute('href', '/signals/2026-09-17');
    expect(days.at(-1)).toHaveAttribute('href', '/signals/2026-09-27');
    // 앞의 사흘은 대화하지 않은 날이라 보여 줄 근거가 없다.
    expect(within(table).queryByRole('link', { name: '9월 14일 월요일' })).not.toBeInTheDocument();
  });

  it('점이 있는 칸도 그날로 가는 링크다. 눈에 띄는 것을 눌렀을 때 아무 일도 없으면 안 된다', async () => {
    serveTrend(healthy);

    renderRoute('/trend');

    const table = await calendar();
    // 줄 이름과 날짜와 표시를 함께 읽어 주어서, 같은 곳으로 가는 링크 넷을 구분할 수 있다.
    const dot = within(table).getByRole('link', { name: '기분 9월 17일 목요일 신호가 보인 날' });
    expect(dot).toHaveAttribute('href', '/signals/2026-09-17');
    expect(
      within(table).getByRole('link', { name: '수면 9월 27일 일요일 그 이야기는 없었던 날' }),
    ).toHaveAttribute('href', '/signals/2026-09-27');
    // 대화하지 않은 날의 칸은 갈 곳이 없다.
    expect(
      within(table).queryByRole('link', { name: /9월 14일 월요일 대화하지 않은 날/ }),
    ).not.toBeInTheDocument();
  });

  it('기준일을 밝히고, 오늘의 분석이 아직 없을 때만 나중에 나타난다고 알려 준다', async () => {
    // 서울은 9월 28일 저녁이다. 기준일(27일)이 어제이므로 오늘 나눈 이야기는 아직 달력에 없다.
    atInstant('2026-09-28T11:00:00.000Z');
    serveTrend(healthy);

    renderRoute('/trend');

    await calendar();
    expect(screen.getByText(/2026년 9월 27일 일요일까지의 기록이에요/)).toHaveAttribute(
      'datetime',
      '2026-09-27',
    );
    expect(
      screen.getByText(/오늘 나눈 이야기는 정리가 끝나면 이 달력에 나타나요/),
    ).toBeInTheDocument();
  });

  it('오늘의 분석이 이미 끝났으면 곧 나타난다고 말하지 않는다', async () => {
    // 서울은 9월 27일 저녁이고 기준일도 27일이다. 오늘 칸에 점이 이미 찍혀 있는데
    // "정리가 끝나면 나타나요"라고 적으면 보는 사람이 그 칸을 믿을 수 없게 된다.
    atInstant('2026-09-27T11:00:00.000Z');
    serveTrend(healthy);

    renderRoute('/trend');

    await calendar();
    expect(screen.getByText('2026년 9월 27일 일요일까지의 기록이에요.')).toBeInTheDocument();
    expect(
      screen.queryByText(/오늘 나눈 이야기는 정리가 끝나면 이 달력에 나타나요/),
    ).not.toBeInTheDocument();
  });
});

describe('추세 화면: 견주는 말', () => {
  it('일수와 판단을 서버가 보낸 값 그대로 쓴다. 칸을 세거나 나누어 다시 구하지 않는다', async () => {
    serveTrend(healthy);

    renderRoute('/trend');

    await calendar();
    // 칸에 찍힌 찬 점은 일곱이지만 서버가 보낸 일수는 아홉이다. 화면은 서버가 보낸 값을 적는다.
    // 분모가 달력의 칸 수(열넷)가 아니라 이야기한 날의 수라는 것을 문장이 함께 말한다.
    expect(
      screen.getByText('이야기한 12일 가운데 9일이에요. 평소보다 잦아요.'),
    ).toBeInTheDocument();
    expect(screen.getByText('평소에는 이야기한 12일 가운데 11일이었어요.')).toBeInTheDocument();
    // 11일 가운데 2일은 산술로는 평소(6일)보다 드물고, 서버의 판단도 드묾이다.
    expect(
      screen.getByText('이야기한 11일 가운데 2일이에요. 평소보다 드물어요.'),
    ).toBeInTheDocument();
    expect(
      screen.getByText('이야기한 11일 가운데 0일이에요. 평소와 비슷해요.'),
    ).toBeInTheDocument();
  });

  it('일수와 견주는 말은 옆으로 미는 달력 밖에 둔다. 폰에서 문장의 앞부분이 잘려 나가면 안 된다', async () => {
    serveTrend(healthy);

    renderRoute('/trend');

    const table = await calendar();
    const sentence = screen.getByText('이야기한 12일 가운데 9일이에요. 평소보다 잦아요.');
    expect(table).not.toContainElement(sentence);
    // 옆으로 미는 상자는 달력만 감싼다. 그 상자가 기준이 되어야 안의 숨은 글이 쪽 전체를 밀지 않는다.
    const scroller = table.parentElement;
    expect(scroller?.className).toContain('overflow-x-auto');
    expect(scroller?.className).toContain('relative');
    expect(scroller).not.toContainElement(sentence);
    // 줄 이름은 달력 안에 그대로 남고(가로로 밀려도 왼쪽에 붙어 있다), 문장 옆에도 함께 적는다.
    expect(within(table).getByRole('rowheader', { name: '기분' })).toBeInTheDocument();
    expect(screen.getAllByText('기분')).toHaveLength(2);
  });
});

describe('추세 화면: 기록이 모자란 상태', () => {
  it('한 번도 이야기하지 않았으면 빈 달력과 함께 첫 대화로 이끈다', async () => {
    serveTrend(noRecords);

    renderRoute('/trend');

    expect(
      await screen.findByRole('heading', { level: 2, name: '아직 기록이 없어요' }),
    ).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '오늘 이야기하기' })).toHaveAttribute('href', '/talk');
    // 고장이 아니라는 것을 보여 주려고 빈 달력과 점 읽는 법을 그대로 둔다.
    const table = await calendar();
    expect(marksOf(table, '기분')).toEqual(
      Array.from({ length: 14 }, () => MARK_TEXT.no_conversation),
    );
    expect(screen.queryByText(COMPARISON_SENTENCE)).not.toBeInTheDocument();
    // 셀 것이 없는 날에 "0일 가운데 0일이에요"를 적으면 셈이 아니라 고장처럼 읽힌다.
    expect(screen.queryByText(/일 가운데 .*일이에요/)).not.toBeInTheDocument();
  });

  it('앞선 기록이 있는 사람에게는 기록이 없다고 말하지 않는다', async () => {
    // 평소가 이미 잡혀 있다는 것은 이 달력의 기간 앞에 기록이 쌓였다는 뜻이다.
    // 두 주를 쉬었다고 "아직 기록이 없어요"라고 말하면 그 사람의 기록을 두고 거짓을 적는 셈이다.
    serveTrend({ ...noRecords, insufficient_records: true, baseline_pending: false });

    renderRoute('/trend');

    expect(
      await screen.findByRole('heading', { level: 2, name: '이 기간에는 기록이 없어요' }),
    ).toBeInTheDocument();
    expect(screen.getByText(/그 앞의 기록은 그대로 있고/)).toBeInTheDocument();
    expect(screen.queryByText('아직 기록이 없어요')).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: '오늘 이야기하기' })).toHaveAttribute('href', '/talk');
  });

  it('이야기한 날이 모자라면 일수만 보여 주고 평소와 견주지 않는다', async () => {
    serveTrend(tooFew);

    renderRoute('/trend');

    expect(
      await screen.findByRole('heading', { level: 2, name: '조금만 더 모아요' }),
    ).toBeInTheDocument();
    // 창 안에서 센 일수다. "지금까지 3일"이라고 적으면 30일 기록한 사람에게도 3일이라고 말하게 된다.
    expect(screen.getByText('최근 14일 가운데 3일 이야기했어요.')).toBeInTheDocument();
    expect(screen.getByText('이야기한 3일 가운데 2일이에요.')).toBeInTheDocument();
    expect(screen.queryByText(COMPARISON_SENTENCE)).not.toBeInTheDocument();
  });

  it('평소가 아직 잡히지 않았으면 그 사실을 말하고 견주는 말을 붙이지 않는다', async () => {
    serveTrend(baselinePending);

    renderRoute('/trend');

    expect(
      await screen.findByRole('heading', { level: 2, name: '평소를 찾고 있어요' }),
    ).toBeInTheDocument();
    expect(screen.getByText('이야기한 12일 가운데 9일이에요.')).toBeInTheDocument();
    expect(screen.getByText('최근 14일 가운데 11일 이야기했어요.')).toBeInTheDocument();
    expect(screen.queryByText(COMPARISON_SENTENCE)).not.toBeInTheDocument();
  });
});

describe('추세 화면: 보여 주지 않는 것', () => {
  // 힘든 사람에게 점수와 경고는 도움보다 상처가 되기 쉽고, 이 값은 검사 결과가 아니라 대화에서 추정한 값이다.
  it.each([
    ['추정 점수', /점수/],
    ['구간', /구간|가벼움|중간|다소 높음|아주 낮음/],
    ['신뢰도', /신뢰도/],
    ['개입 단계', /단계|일상|회고|제안|권유/],
  ])('화면에 %s가 글로도 나오지 않는다', async (_label, forbidden) => {
    serveTrend(healthy);

    renderRoute('/trend');

    await calendar();
    expect(document.body.textContent ?? '').not.toMatch(forbidden);
  });

  it('검사 결과가 아니라는 것과 의료 서비스가 아니라는 것을 함께 밝힌다', async () => {
    serveTrend(healthy);

    renderRoute('/trend');

    await calendar();
    expect(
      screen.getByText('대화에서 읽어 낸 이야기예요. 검사나 진단의 결과가 아니에요.'),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        '내일은 의료 서비스가 아니며, 제공되는 정보는 진단이나 치료를 대신하지 않습니다.',
      ),
    ).toBeInTheDocument();
  });
});

describe('추세 화면: 불러오지 못했을 때', () => {
  it('까닭을 알리고 다시 불러올 수 있게 한다', async () => {
    let online = false;
    mockApi({
      'GET /api/v1/me': signedIn,
      'GET /api/v1/trend': () => (online ? json(200, healthy) : networkFailure()),
    });

    renderRoute('/trend');

    expect(await screen.findByRole('alert')).toHaveTextContent('인터넷 연결이 고르지 않아요.');
    online = true;
    await userEvent.click(screen.getByRole('button', { name: '다시 불러오기' }));

    expect(await calendar()).toBeInTheDocument();
  });
});
