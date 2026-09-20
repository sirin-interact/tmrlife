import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { json, mockApi, networkFailure, problem, signedIn } from '@/test/mockApi';
import { renderRoute } from '@/test/renderRoute';

const september = {
  items: [
    { date: '2026-09-18', status: 'confirmed', first_line: '발표가 끝났다.' },
    { date: '2026-09-20', status: 'draft', first_line: '오늘은 친구랑 한강에 놀러 갔다 왔다.' },
  ],
};

function useSeptember20th() {
  // 서울의 9월 20일 저녁 8시
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(new Date('2026-09-20T11:00:00.000Z'));
}

afterEach(() => {
  vi.useRealTimers();
});

describe('일기장: 달마다 보기', () => {
  it('이번 달(기록 날짜 기준)의 일기를 최근 날부터 보여 주고, 확인 전인 초안을 표시한다', async () => {
    useSeptember20th();
    const api = mockApi({
      'GET /api/v1/me': signedIn,
      'GET /api/v1/diaries': () => json(200, september),
    });

    renderRoute('/diary');

    expect(
      await screen.findByRole('heading', { level: 2, name: '2026년 9월' }),
    ).toBeInTheDocument();
    const entries = await screen.findAllByRole('link', { name: /9월 (18|20)일.*(갔다|끝났다)/ });
    expect(entries.map((entry) => entry.getAttribute('href'))).toEqual([
      '/diary/2026-09-20',
      '/diary/2026-09-18',
    ]);
    expect(within(entries[0]!).getByText('확인 전')).toBeInTheDocument();
    expect(within(entries[1]!).queryByText('확인 전')).not.toBeInTheDocument();
    expect(api.callsTo('GET /api/v1/diaries')[0]?.query).toEqual({ month: '2026-09' });
    expect(document.title).toBe('일기장 · 내일');
  });

  it('새벽 4시 전에는 전날이 속한 달을 연다', async () => {
    // 서울의 10월 1일 새벽 2시. 기록 날짜로는 아직 9월 30일이다.
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-09-30T17:00:00.000Z'));
    const api = mockApi({
      'GET /api/v1/me': signedIn,
      'GET /api/v1/diaries': () => json(200, { items: [] }),
    });

    renderRoute('/diary');

    await screen.findByRole('heading', { level: 2, name: '2026년 9월' });
    await waitFor(() =>
      expect(api.callsTo('GET /api/v1/diaries')[0]?.query).toEqual({ month: '2026-09' }),
    );
  });

  it('달력에서 일기가 있는 날만 누를 수 있다', async () => {
    useSeptember20th();
    mockApi({ 'GET /api/v1/me': signedIn, 'GET /api/v1/diaries': () => json(200, september) });

    renderRoute('/diary');

    const calendar = await screen.findByRole('table', { name: '일기가 있는 날' });
    const days = within(calendar).getAllByRole('link');
    expect(days.map((day) => day.getAttribute('href'))).toEqual([
      '/diary/2026-09-18',
      '/diary/2026-09-20',
    ]);
    expect(days[0]).toHaveAccessibleName('9월 18일 금요일, 일기 있음');
    expect(days[1]).toHaveAccessibleName('9월 20일 일요일, 확인 전 초안 있음');
  });

  it('이전 달로 옮길 수 있고, 이번 달에서는 다음 달로 가는 길을 두지 않는다', async () => {
    useSeptember20th();
    const api = mockApi({
      'GET /api/v1/me': signedIn,
      'GET /api/v1/diaries': (request) =>
        json(
          200,
          new URL(request.url).searchParams.get('month') === '2026-09' ? september : { items: [] },
        ),
    });
    renderRoute('/diary');
    const user = userEvent.setup();
    await screen.findByRole('heading', { level: 2, name: '2026년 9월' });
    expect(screen.queryByRole('link', { name: '다음 달' })).not.toBeInTheDocument();

    await user.click(screen.getByRole('link', { name: '이전 달' }));

    expect(
      await screen.findByRole('heading', { level: 2, name: '2026년 8월' }),
    ).toBeInTheDocument();
    expect(await screen.findByText('이 달에는 아직 일기가 없어요.')).toBeInTheDocument();
    expect(api.callsTo('GET /api/v1/diaries').at(-1)?.query).toEqual({ month: '2026-08' });
    expect(screen.getByRole('link', { name: '다음 달' })).toHaveAttribute(
      'href',
      '/diary?month=2026-09',
    );
  });

  it.each(['2026-13', 'next', '2099-01'])(
    '주소의 달(%s)이 틀렸거나 아직 오지 않은 달이면 이번 달을 보여 준다',
    async (month) => {
      useSeptember20th();
      const api = mockApi({
        'GET /api/v1/me': signedIn,
        'GET /api/v1/diaries': () => json(200, { items: [] }),
      });

      renderRoute(`/diary?month=${month}`);

      await screen.findByRole('heading', { level: 2, name: '2026년 9월' });
      await waitFor(() =>
        expect(api.callsTo('GET /api/v1/diaries')[0]?.query).toEqual({ month: '2026-09' }),
      );
    },
  );

  it('목록을 불러오지 못하면 알리고 다시 불러올 수 있게 한다', async () => {
    useSeptember20th();
    let online = false;
    mockApi({
      'GET /api/v1/me': signedIn,
      'GET /api/v1/diaries': () => (online ? json(200, september) : networkFailure()),
    });
    renderRoute('/diary');

    expect(await screen.findByRole('alert')).toHaveTextContent('인터넷 연결이 고르지 않아요.');
    online = true;
    await userEvent.click(screen.getByRole('button', { name: '다시 불러오기' }));

    expect(await screen.findByText('발표가 끝났다.')).toBeInTheDocument();
  });
});

describe('일기장: 찾기', () => {
  it('찾는 말을 서버에 보내고, 결과를 해까지 적어 보여 준다. 찾는 말은 주소에 남기지 않는다', async () => {
    useSeptember20th();
    const api = mockApi({
      'GET /api/v1/me': signedIn,
      'GET /api/v1/diaries': (request) =>
        new URL(request.url).searchParams.has('q')
          ? json(200, {
              items: [
                {
                  date: '2025-12-31',
                  status: 'confirmed',
                  first_line: '한 해의 마지막 날.',
                  snippet: '…친구랑 한강에서 해넘이를 봤다…',
                },
              ],
            })
          : json(200, september),
    });
    const { router } = renderRoute('/diary');
    const user = userEvent.setup();

    await user.type(await screen.findByLabelText('일기에서 찾기'), '  한강  {Enter}');

    expect(await screen.findByText('1개의 일기를 찾았어요.')).toBeInTheDocument();
    const result = screen.getByRole('link', { name: /2025년 12월 31일 수요일/ });
    expect(result).toHaveAttribute('href', '/diary/2025-12-31');
    expect(result).toHaveTextContent('…친구랑 한강에서 해넘이를 봤다…');
    expect(api.callsTo('GET /api/v1/diaries').at(-1)?.query).toEqual({ q: '한강' });
    expect(router.state.location.search).toBe('');
    // 찾는 동안에는 달력을 보여 주지 않는다.
    expect(screen.queryByRole('table')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '검색 지우기' }));
    expect(await screen.findByRole('table', { name: '일기가 있는 날' })).toBeInTheDocument();
    expect(screen.getByLabelText('일기에서 찾기')).toHaveValue('');
  });

  it('맞는 일기가 없으면 없다고 알린다', async () => {
    useSeptember20th();
    mockApi({
      'GET /api/v1/me': signedIn,
      'GET /api/v1/diaries': (request) =>
        json(200, new URL(request.url).searchParams.has('q') ? { items: [] } : september),
    });
    renderRoute('/diary');

    await userEvent.type(await screen.findByLabelText('일기에서 찾기'), '없는 말{Enter}');

    expect(await screen.findByText('찾는 말이 들어 있는 일기가 없어요.')).toBeInTheDocument();
  });

  it('빈 말과 너무 긴 말로는 서버에 묻지 않는다', async () => {
    useSeptember20th();
    const api = mockApi({
      'GET /api/v1/me': signedIn,
      'GET /api/v1/diaries': () => json(200, september),
    });
    renderRoute('/diary');
    const user = userEvent.setup();
    const box = await screen.findByLabelText('일기에서 찾기');

    await user.type(box, '   {Enter}');
    expect(box).toHaveAccessibleDescription('찾고 싶은 말을 입력해 주세요.');

    await user.clear(box);
    await user.click(box);
    await user.paste('가'.repeat(101));
    await user.keyboard('{Enter}');
    expect(box).toHaveAccessibleDescription('찾는 말은 100자까지 쓸 수 있어요.');

    expect(api.callsTo('GET /api/v1/diaries').every((call) => !('q' in call.query))).toBe(true);
  });

  it('검색이 실패하면 알린다', async () => {
    useSeptember20th();
    mockApi({
      'GET /api/v1/me': signedIn,
      'GET /api/v1/diaries': (request) =>
        new URL(request.url).searchParams.has('q')
          ? problem(429, 'rate_limited', {}, { 'Retry-After': '30' })
          : json(200, september),
    });
    renderRoute('/diary');

    await userEvent.type(await screen.findByLabelText('일기에서 찾기'), '한강{Enter}');

    expect(await screen.findByRole('alert')).toHaveTextContent('30초 뒤에 다시 시도해 주세요.');
  });
});
