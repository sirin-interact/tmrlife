import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { DaySignalItem, DaySignals, Diary, SignalItem, SignalStatus } from '@/api/types';
import {
  EVIDENCE_TEXT,
  explicitnessLabel,
  itemLabel,
  judgementLabel,
  NOT_MENTIONED_LABEL,
} from '@/content/evidenceText';
import { POLL_INTERVAL_MS } from '@/evidence/queries';
import { json, mockApi, networkFailure, problem, signedIn, testMe } from '@/test/mockApi';
import { renderRoute } from '@/test/renderRoute';

const DATE = '2026-09-20';
const SIGNALS_PATH = `/api/v1/days/${DATE}/signals` as const;
const DIARY_PATH = `/api/v1/diaries/${DATE}` as const;
const CANCEL_PATH = `/api/v1/signals/sleep-row-1/cancel` as const;
const UNCANCEL_PATH = `/api/v1/signals/sleep-row-1/uncancel` as const;
const CONVERSATION = '0199a2b3-c4d5-7e6f-8a9b-0c1d2e3f4a5b';
const OTHER_CONVERSATION = '0199a2b3-c4d5-7e6f-8a9b-0c1d2e3f4a5c';

// 근거로 오는 말. 서버가 대화 기록과 글자 그대로 대조한 문장이라 화면이 손대면 안 된다.
// 마지막 것은 앞뒤와 가운데에 공백이 있다. 화면이 다듬지 않는지 보려는 값이다.
const QUOTE = {
  interest: '게임도 켜놓고 그냥 껐어, 재미가 없더라',
  mood: '그냥 계속 멍하게 있었어',
  sleep: '새벽 4시까지 뒤척이다가 겨우 잤어',
  fatigue: '오늘은 좀 괜찮았어, 산책도 했고',
  selfBlame: '내가 다 망친 것 같아',
  concentrationMorning: '보고서를 세 줄 쓰다 말았어',
  concentrationAfternoon: '오후엔 집중이 좀 됐어',
  psychomotor: '  발을 계속 떨고 있었대,  나도 몰랐어  ',
};

/** 여덟 항목이 모든 판단과 명시성 조합을 한 번씩 담은 하루. */
const ITEMS: DaySignalItem[] = [
  {
    item: 'interest',
    status: 'observed',
    explicitness: 'direct',
    rows: [
      {
        id: 'interest-row',
        conversation_id: CONVERSATION,
        status: 'observed',
        explicitness: 'direct',
        evidence: QUOTE.interest,
        utterance_seq: 4,
        cancelled: false,
      },
    ],
  },
  {
    item: 'mood',
    status: 'observed',
    explicitness: 'indirect',
    rows: [
      {
        id: 'mood-row',
        conversation_id: CONVERSATION,
        status: 'observed',
        explicitness: 'indirect',
        evidence: QUOTE.mood,
        cancelled: false,
      },
    ],
  },
  {
    item: 'sleep',
    status: 'observed',
    explicitness: 'direct',
    rows: [
      {
        id: 'sleep-row-1',
        conversation_id: CONVERSATION,
        status: 'observed',
        explicitness: 'direct',
        evidence: QUOTE.sleep,
        cancelled: false,
      },
    ],
  },
  {
    item: 'fatigue',
    status: 'not_observed',
    explicitness: 'direct',
    rows: [
      {
        id: 'fatigue-row',
        conversation_id: CONVERSATION,
        status: 'not_observed',
        explicitness: 'direct',
        evidence: QUOTE.fatigue,
        cancelled: false,
      },
    ],
  },
  {
    item: 'appetite',
    status: 'not_mentioned',
    explicitness: 'none',
    rows: [
      {
        id: 'appetite-row',
        conversation_id: CONVERSATION,
        status: 'not_mentioned',
        explicitness: 'none',
        evidence: null,
        cancelled: false,
      },
    ],
  },
  {
    // 사용자가 이미 아니라고 한 항목이다. 남은 판단이 없으니 서버가 언급 없음으로 합쳐서 보낸다.
    item: 'self_blame',
    status: 'not_mentioned',
    explicitness: 'none',
    rows: [
      {
        id: 'self-blame-row',
        conversation_id: CONVERSATION,
        status: 'observed',
        explicitness: 'direct',
        evidence: QUOTE.selfBlame,
        cancelled: true,
      },
    ],
  },
  {
    // 하루에 두 번 이야기한 항목. 한 번이라도 관찰됐으면 관찰됨이다.
    item: 'concentration',
    status: 'observed',
    explicitness: 'indirect',
    rows: [
      {
        id: 'concentration-row-1',
        conversation_id: CONVERSATION,
        status: 'observed',
        explicitness: 'indirect',
        evidence: QUOTE.concentrationMorning,
        cancelled: false,
      },
      {
        id: 'concentration-row-2',
        conversation_id: OTHER_CONVERSATION,
        status: 'not_observed',
        explicitness: 'direct',
        evidence: QUOTE.concentrationAfternoon,
        cancelled: false,
      },
    ],
  },
  {
    item: 'psychomotor',
    status: 'not_observed',
    explicitness: 'indirect',
    rows: [
      {
        id: 'psychomotor-row',
        conversation_id: CONVERSATION,
        status: 'not_observed',
        explicitness: 'indirect',
        evidence: QUOTE.psychomotor,
        cancelled: false,
      },
    ],
  },
];

const analysedDay: DaySignals = { date: DATE, analysed: true, items: ITEMS };
const waitingDay: DaySignals = { date: DATE, analysed: false, items: [] };

/** 서버가 잠 항목의 취소를 반영해 다시 합쳐 보내는 하루. 화면의 계산과 따로 적어 둔다. */
function dayWithSleepCancelled(cancelled: boolean): DaySignals {
  return {
    ...analysedDay,
    items: ITEMS.map((item) =>
      item.item === 'sleep'
        ? {
            ...item,
            status: cancelled ? 'not_mentioned' : 'observed',
            explicitness: cancelled ? 'none' : 'direct',
            rows: item.rows.map((row) => ({ ...row, cancelled })),
          }
        : item,
    ),
  };
}

function diaryAt(updatedAt: string): Diary {
  return {
    date: DATE,
    status: 'draft',
    text: '오늘은 늦게까지 잠이 오지 않았다.',
    confirmed_at: null,
    updated_at: updatedAt,
  };
}

const oldDiary = () => json(200, diaryAt('2026-09-20T13:00:00.000Z'));
const freshDiary = () => json(200, diaryAt(new Date(Date.now() - 5_000).toISOString()));
const noDiary = () => problem(404, 'not_found');

function open(routes: Parameters<typeof mockApi>[0] = {}) {
  const api = mockApi({
    'GET /api/v1/me': signedIn,
    [`GET ${SIGNALS_PATH}`]: () => json(200, analysedDay),
    [`GET ${DIARY_PATH}`]: oldDiary,
    ...routes,
  });
  const rendered = renderRoute(`/signals/${DATE}`);
  return { api, ...rendered };
}

/** 항목 하나의 카드. 항목 이름이 그 카드의 제목이다. */
function itemCard(label: string): HTMLElement {
  const heading = screen.getByRole('heading', { level: 2, name: label });
  const card = heading.closest('li');
  if (card === null) throw new Error(`항목 카드를 찾지 못했다: ${label}`);
  return card;
}

/** 카드에 따온 말. 공백까지 그대로 읽는다(getByText는 공백을 정리해 버린다). */
function quotesIn(card: HTMLElement): string[] {
  return Array.from(card.querySelectorAll('q'), (node) => node.textContent ?? '');
}

/** 여덟 항목이 그려질 때까지 기다린다. 날짜와 링크는 값이 오기 전에도 떠 있으므로 그것으로는 기다릴 수 없다. */
function itemsShown() {
  return screen.findByRole('heading', { level: 2, name: '즐거움' });
}

afterEach(() => {
  vi.useRealTimers();
});

describe('근거 화면: 여덟 항목과 판단', () => {
  it('여덟 항목을 모두 보여 주고 판단을 우리말 한 줄로 적는다', async () => {
    open();
    await itemsShown();

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('2026년 9월 20일 일요일');
    expect(screen.getAllByRole('heading', { level: 2 })).toHaveLength(8);
    expect(document.title).toBe('9월 20일 일요일의 마음 신호 · 내일');

    expect(within(itemCard('즐거움')).getByText('즐거운 일이 줄었어요')).toBeVisible();
    expect(within(itemCard('기분')).getByText('기분이 가라앉았어요')).toBeVisible();
    expect(within(itemCard('잠')).getByText('잠이 편하지 않았어요')).toBeVisible();
    expect(within(itemCard('기운')).getByText('기운은 괜찮았어요')).toBeVisible();
    expect(within(itemCard('집중')).getByText('집중하기 어려웠어요')).toBeVisible();
    expect(within(itemCard('몸의 움직임')).getByText('몸은 편했어요')).toBeVisible();
  });

  it('직접 말한 판단과 미루어 본 판단을 구분해 보여 준다', async () => {
    open();
    await itemsShown();

    expect(within(itemCard('잠')).getByText('직접 말한 내용')).toBeVisible();
    expect(within(itemCard('기운')).getByText('직접 말한 내용')).toBeVisible();
    expect(within(itemCard('기분')).getByText('말에서 미루어 본 내용')).toBeVisible();
    expect(within(itemCard('몸의 움직임')).getByText('말에서 미루어 본 내용')).toBeVisible();
  });

  it('근거가 된 말은 글자 하나 고치지 않고 그대로 따온다', async () => {
    open();
    await itemsShown();

    expect(quotesIn(itemCard('즐거움'))).toEqual([QUOTE.interest]);
    expect(quotesIn(itemCard('잠'))).toEqual([QUOTE.sleep]);
    // 앞뒤와 가운데의 공백까지 그대로다.
    expect(quotesIn(itemCard('몸의 움직임'))).toEqual([QUOTE.psychomotor]);
  });

  it('하루에 두 번 이야기한 항목은 대화마다 근거와 그때의 판단을 함께 보여 준다', async () => {
    open();
    await itemsShown();
    const card = itemCard('집중');

    expect(quotesIn(card)).toEqual([QUOTE.concentrationMorning, QUOTE.concentrationAfternoon]);
    // 합친 판단과 다른 행에만 그 행의 판단을 적는다.
    expect(within(card).getByText('집중은 잘 됐어요')).toBeVisible();
    expect(within(card).getAllByText('집중하기 어려웠어요')).toHaveLength(1);
  });

  it('이야기가 없었던 항목은 "말하지 않음"으로만 보여 주고, 아니라고 할 것도 없다', async () => {
    open();
    await itemsShown();
    const card = itemCard('입맛');

    expect(within(card).getByText(NOT_MENTIONED_LABEL)).toBeVisible();
    expect(quotesIn(card)).toEqual([]);
    expect(within(card).queryByRole('button')).not.toBeInTheDocument();
    // 빠뜨렸다는 안내나 사과를 붙이지 않는다.
    expect(card.textContent).toBe(`입맛${NOT_MENTIONED_LABEL}`);
  });

  it('이미 아니라고 한 판단은 "말하지 않음"이 아니라 빼 두었다고 적고, 근거는 그대로 남긴다', async () => {
    open();
    await itemsShown();
    const card = itemCard('나를 보는 마음');

    expect(within(card).getAllByText(EVIDENCE_TEXT.cancelledBadge).length).toBeGreaterThan(0);
    expect(within(card).queryByText(NOT_MENTIONED_LABEL)).not.toBeInTheDocument();
    expect(quotesIn(card)).toEqual([QUOTE.selfBlame]);
    expect(within(card).getByText(EVIDENCE_TEXT.cancelledNote)).toBeVisible();
    expect(
      within(card).getByRole('button', { name: EVIDENCE_TEXT.undoFor('나를 보는 마음') }),
    ).toBeEnabled();
  });

  it('무엇이 함께 바뀌는지 적어 두고, 있지도 않은 숫자를 말하지 않는다', async () => {
    open();
    await itemsShown();

    expect(screen.getByText(EVIDENCE_TEXT.cancelNote)).toBeVisible();
  });

  it('불러오지 못하면 다시 불러올 수 있다', async () => {
    // 조회 계층이 서버 쪽 실패를 한 번 더 시도한다. 사람에게 물어보는 자리를 보려면 그 한 번까지 실패해야 한다.
    let attempts = 0;
    const api = open({
      [`GET ${SIGNALS_PATH}`]: () => {
        attempts += 1;
        return attempts <= 2 ? problem(500, 'internal_error') : json(200, analysedDay);
      },
    }).api;
    const user = userEvent.setup();

    await user.click(await screen.findByRole('button', { name: EVIDENCE_TEXT.retry }));

    expect(await screen.findByRole('heading', { level: 2, name: '잠' })).toBeVisible();
    expect(api.callsTo(`GET ${SIGNALS_PATH}`).length).toBeGreaterThanOrEqual(2);
  });
});

describe('근거 화면: 이건 아니에요', () => {
  it('누를 수 있는 것으로 보인다. 옆의 알약과 같은 회색 라벨로 읽히면 안 된다', async () => {
    open();
    await itemsShown();

    const cancel = screen.getByRole('button', { name: EVIDENCE_TEXT.cancelFor('잠') });
    // 테두리가 있는 모양(outline)이라 옆에 놓인 "직접 말한 내용" 알약과 다른 것으로 읽힌다.
    expect(cancel).toHaveAttribute('data-variant', 'outline');
    expect(cancel.className).not.toContain('text-muted-foreground');
  });

  it('누르면 답을 기다리지 않고 기록에서 뺀 것으로 보여 주고, 서버의 답으로 덮는다', async () => {
    let release: (() => void) | undefined;
    const api = open({
      [`POST ${CANCEL_PATH}`]: async () => {
        await new Promise<void>((resolve) => {
          release = resolve;
        });
        return json(200, dayWithSleepCancelled(true));
      },
    }).api;
    const user = userEvent.setup();
    await itemsShown();

    await user.click(screen.getByRole('button', { name: EVIDENCE_TEXT.cancelFor('잠') }));

    // 답이 오기 전에 이미 바뀌어 있다.
    const card = itemCard('잠');
    expect(within(card).getAllByText(EVIDENCE_TEXT.cancelledBadge).length).toBeGreaterThan(0);
    expect(within(card).getByText(EVIDENCE_TEXT.cancelledNote)).toBeVisible();
    expect(api.callsTo(`POST ${CANCEL_PATH}`)).toHaveLength(1);

    release?.();

    await waitFor(() =>
      expect(within(itemCard('잠')).queryByText('잠이 편하지 않았어요')).not.toBeNull(),
    );
    // 따온 말은 취소한 뒤에도 그대로 남는다. 왜 그렇게 봤는지 나중에도 되짚을 수 있어야 한다.
    expect(quotesIn(itemCard('잠'))).toEqual([QUOTE.sleep]);
    expect(
      within(itemCard('잠')).getByRole('button', { name: EVIDENCE_TEXT.undoFor('잠') }),
    ).toBeEnabled();
  });

  it('되돌리기를 누르면 다시 기록에 넣는다', async () => {
    const api = open({
      [`GET ${SIGNALS_PATH}`]: () => json(200, dayWithSleepCancelled(true)),
      [`POST ${UNCANCEL_PATH}`]: () => json(200, dayWithSleepCancelled(false)),
    }).api;
    const user = userEvent.setup();
    await itemsShown();

    await user.click(screen.getByRole('button', { name: EVIDENCE_TEXT.undoFor('잠') }));

    await waitFor(() =>
      expect(
        within(itemCard('잠')).queryByRole('button', { name: EVIDENCE_TEXT.cancelFor('잠') }),
      ).not.toBeNull(),
    );
    const card = itemCard('잠');
    expect(within(card).getByText('잠이 편하지 않았어요')).toBeVisible();
    expect(within(card).queryByText(EVIDENCE_TEXT.cancelledNote)).not.toBeInTheDocument();
    expect(api.callsTo(`POST ${UNCANCEL_PATH}`)).toHaveLength(1);
  });

  it('보내지 못하면 눌렀던 것을 되돌리고 조용히 알린다', async () => {
    open({ [`POST ${CANCEL_PATH}`]: networkFailure });
    const user = userEvent.setup();
    await itemsShown();

    await user.click(screen.getByRole('button', { name: EVIDENCE_TEXT.cancelFor('잠') }));

    expect(await screen.findByText(EVIDENCE_TEXT.cancelFailed)).toBeVisible();
    const card = itemCard('잠');
    expect(within(card).getByText('잠이 편하지 않았어요')).toBeVisible();
    expect(within(card).queryByText(EVIDENCE_TEXT.cancelledBadge)).not.toBeInTheDocument();
    expect(within(card).getByRole('button', { name: EVIDENCE_TEXT.cancelFor('잠') })).toBeEnabled();
  });

  it('이미 없는 기록이면 다시 눌러 보라고 하지 않는다', async () => {
    open({ [`POST ${CANCEL_PATH}`]: () => problem(404, 'not_found') });
    const user = userEvent.setup();
    await itemsShown();

    await user.click(screen.getByRole('button', { name: EVIDENCE_TEXT.cancelFor('잠') }));

    expect(await screen.findByText(EVIDENCE_TEXT.cancelGone)).toBeVisible();
    expect(within(itemCard('잠')).getByText('잠이 편하지 않았어요')).toBeVisible();
  });
});

describe('근거 화면: 아직 볼 것이 없는 날', () => {
  it('이야기를 나누지 않은 날은 그렇게만 말한다', async () => {
    open({ [`GET ${SIGNALS_PATH}`]: () => json(200, waitingDay), [`GET ${DIARY_PATH}`]: noDiary });

    expect(
      await screen.findByRole('heading', { level: 2, name: EVIDENCE_TEXT.noConversationTitle }),
    ).toBeVisible();
    expect(screen.getByText(EVIDENCE_TEXT.noConversationBody)).toBeVisible();
    expect(screen.queryByRole('button', { name: EVIDENCE_TEXT.refresh })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: EVIDENCE_TEXT.toDiary })).not.toBeInTheDocument();
  });

  it('오늘인데 아직 나눈 이야기가 없으면, 방금 마친 이야기일 수도 있다고 덧붙인다', async () => {
    // 서울의 오늘이 기록 날짜와 같은 시각으로 맞춘다.
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-09-20T12:00:00.000Z'));
    open({ [`GET ${SIGNALS_PATH}`]: () => json(200, waitingDay), [`GET ${DIARY_PATH}`]: noDiary });

    expect(
      await screen.findByRole('heading', { level: 2, name: EVIDENCE_TEXT.noConversationTitle }),
    ).toBeVisible();
    expect(screen.getByText(EVIDENCE_TEXT.justTalkedHint)).toBeVisible();
    expect(screen.getByRole('button', { name: EVIDENCE_TEXT.refresh })).toBeEnabled();
    expect(screen.getByRole('link', { name: EVIDENCE_TEXT.toTalk })).toHaveAttribute(
      'href',
      '/talk',
    );
  });

  it('방금 나눈 이야기를 정리하는 중이면 기다리면 된다고 말한다', async () => {
    open({
      [`GET ${SIGNALS_PATH}`]: () => json(200, waitingDay),
      [`GET ${DIARY_PATH}`]: freshDiary,
    });

    expect(
      await screen.findByRole('heading', { level: 2, name: EVIDENCE_TEXT.pendingTitle }),
    ).toBeVisible();
    expect(screen.getByText(EVIDENCE_TEXT.pendingBody)).toBeVisible();
    expect(screen.getByRole('link', { name: EVIDENCE_TEXT.toDiary })).toHaveAttribute(
      'href',
      `/diary/${DATE}`,
    );
  });

  it('정리가 끝나면 기다리는 사람이 다시 누르지 않아도 나타난다', async () => {
    // 저절로 다시 물어보는 간격을 앞당겨 보려고 시계를 바꿔 끼운다. 화면을 그리기 전에 끼워야 그 타이머도 함께 잡힌다.
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let analysed = false;
    const api = open({
      [`GET ${SIGNALS_PATH}`]: () => json(200, analysed ? analysedDay : waitingDay),
      [`GET ${DIARY_PATH}`]: freshDiary,
    }).api;
    await screen.findByRole('heading', { level: 2, name: EVIDENCE_TEXT.pendingTitle });

    analysed = true;
    await act(() => vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS));

    await waitFor(() => expect(screen.queryByRole('heading', { name: '잠' })).not.toBeNull());
    expect(api.callsTo(`GET ${SIGNALS_PATH}`).length).toBeGreaterThanOrEqual(2);
  });

  it('오래 기다렸는데 끝나지 않았으면 기다리기를 그만두고 다시 확인할 수 있게 한다', async () => {
    let analysed = false;
    const api = open({
      [`GET ${SIGNALS_PATH}`]: () => json(200, analysed ? analysedDay : waitingDay),
      // 대화는 한참 전에 끝났다. 그런데도 정리된 신호가 없다.
      [`GET ${DIARY_PATH}`]: oldDiary,
    }).api;
    const user = userEvent.setup();

    expect(
      await screen.findByRole('heading', { level: 2, name: EVIDENCE_TEXT.stalledTitle }),
    ).toBeVisible();
    expect(screen.getByText(EVIDENCE_TEXT.stalledBody)).toBeVisible();
    // 저절로 다시 물어보지 않는다. 끝없이 도는 표시를 남기지 않는다.
    expect(api.callsTo(`GET ${SIGNALS_PATH}`)).toHaveLength(1);

    analysed = true;
    await user.click(screen.getByRole('button', { name: EVIDENCE_TEXT.refresh }));

    expect(await screen.findByRole('heading', { level: 2, name: '잠' })).toBeVisible();
  });

  it('마음 신호 읽기를 꺼 두었으면 그 사실을 말하고 기다리지 않는다', async () => {
    const api = open({
      'GET /api/v1/me': () =>
        json(200, { ...testMe, settings: { ...testMe.settings, analysis_enabled: false } }),
      [`GET ${SIGNALS_PATH}`]: () => json(200, waitingDay),
      [`GET ${DIARY_PATH}`]: freshDiary,
    }).api;

    expect(
      await screen.findByRole('heading', { level: 2, name: EVIDENCE_TEXT.disabledTitle }),
    ).toBeVisible();
    expect(screen.getByText(EVIDENCE_TEXT.disabledBody)).toBeVisible();
    expect(api.callsTo(`GET ${SIGNALS_PATH}`)).toHaveLength(1);
  });
});

describe('근거 화면: 오가는 길과 문구', () => {
  it('그날의 일기와 추세 화면으로 갈 수 있다', async () => {
    open();

    expect(await screen.findByRole('link', { name: EVIDENCE_TEXT.toDiary })).toHaveAttribute(
      'href',
      `/diary/${DATE}`,
    );
    expect(screen.getByRole('link', { name: EVIDENCE_TEXT.backToTrend })).toHaveAttribute(
      'href',
      '/trend',
    );
  });

  it('달력에 없는 날짜로 열면 없는 화면으로 답한다', async () => {
    mockApi({ 'GET /api/v1/me': signedIn });

    renderRoute('/signals/2026-02-30');

    expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent(
      '페이지를 찾지 못했어요',
    );
  });
});

// 화면 문구에서 가려야 할 말. 이 앱은 무엇을 판정하지 않는다.
const AVOIDED_WORDS = ['우울', '진단', '판정', '검사', '위험도', '환자', '증상', '치료', '상담'];
const ALL_ITEMS: SignalItem[] = [
  'interest',
  'mood',
  'sleep',
  'fatigue',
  'appetite',
  'self_blame',
  'concentration',
  'psychomotor',
];
const STATUSES: SignalStatus[] = ['observed', 'not_observed', 'not_mentioned'];

describe('근거 화면의 말', () => {
  it('문구에 가려야 할 말이 없다', () => {
    const copy = [
      ...Object.values(EVIDENCE_TEXT).map((value) =>
        typeof value === 'function' ? value('무엇') : value,
      ),
      ...ALL_ITEMS.flatMap((item) => [
        itemLabel(item),
        ...STATUSES.map((status) => judgementLabel(item, status)),
      ]),
      explicitnessLabel('direct') ?? '',
      explicitnessLabel('indirect') ?? '',
    ];

    for (const line of copy) {
      for (const word of AVOIDED_WORDS) {
        expect(line, `"${line}"에 "${word}"가 들어 있다`).not.toContain(word);
      }
    }
  });

  it('그려진 화면에도 가려야 할 말이 없다', async () => {
    open();
    await itemsShown();

    const text = document.body.textContent ?? '';
    for (const word of AVOIDED_WORDS) {
      expect(text).not.toContain(word);
    }
  });
});
