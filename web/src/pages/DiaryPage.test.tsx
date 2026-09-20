import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';

import type { Diary } from '@/api/types';
import { json, mockApi, networkFailure, noContent, problem, signedIn } from '@/test/mockApi';
import { renderRoute } from '@/test/renderRoute';

const DATE = '2026-09-20';
const PATH = `/api/v1/diaries/${DATE}` as const;
const DAY_PATH = `/api/v1/days/${DATE}` as const;

const draft: Diary = {
  date: DATE,
  status: 'draft',
  text: '오늘은 친구랑 한강에 놀러 갔다 왔다.',
  confirmed_at: null,
  updated_at: '2026-09-20T12:00:05.000Z',
};

const confirmed: Diary = {
  date: DATE,
  status: 'confirmed',
  text: '오늘은 친구랑 한강에 놀러 갔다 왔다. 바람이 좋았다.',
  confirmed_at: '2026-09-20T12:10:00.000Z',
  updated_at: '2026-09-20T12:10:00.000Z',
};

const editor = () => screen.getByLabelText('일기');

describe('일기 확인: 초안', () => {
  it('대화에서 넘어오면 초안이 준비됐다고 알리고, 초안을 고칠 수 있는 자리에 담아 보여 준다', async () => {
    mockApi({ 'GET /api/v1/me': signedIn, [`GET ${PATH}`]: () => json(200, draft) });

    renderRoute({ pathname: `/diary/${DATE}`, state: { fromTalk: true } });

    expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent(
      '2026년 9월 20일 일요일',
    );
    expect(await screen.findByText('오늘의 일기 초안이 준비됐어요.')).toBeInTheDocument();
    expect(editor()).toHaveValue(draft.text);
    expect(screen.getByText(/읽어 보고 고친 뒤 저장해 주세요/)).toBeInTheDocument();
    expect(document.title).toBe('9월 20일 일요일의 일기 · 내일');
  });

  it('고쳐도 저절로 저장하지 않는다. 저장을 눌러야 고친 글을 보내고, 확인한 일기로 바뀐다', async () => {
    const api = mockApi({
      'GET /api/v1/me': signedIn,
      [`GET ${PATH}`]: () => json(200, draft),
      [`PUT ${PATH}`]: async (request: Request) => {
        const { text } = (await request.json()) as { text: string };
        return json(200, { ...confirmed, text });
      },
    });
    renderRoute(`/diary/${DATE}`);
    const user = userEvent.setup();
    await screen.findByLabelText('일기');

    await user.type(editor(), ' 바람이 좋았다.  ');
    expect(api.callsTo(`PUT ${PATH}`)).toHaveLength(0);

    await user.click(screen.getByRole('button', { name: '저장' }));

    const saved = await screen.findByText('일기를 저장했어요.');
    await waitFor(() => expect(saved).toHaveFocus());
    expect(api.callsTo(`PUT ${PATH}`)).toHaveLength(1);
    // 앞뒤 공백은 떼고 보낸다.
    expect(api.callsTo(`PUT ${PATH}`)[0]?.body).toEqual({
      text: '오늘은 친구랑 한강에 놀러 갔다 왔다. 바람이 좋았다.',
    });
    expect(screen.queryByLabelText('일기')).not.toBeInTheDocument();
    expect(screen.getByText('오늘은 친구랑 한강에 놀러 갔다 왔다. 바람이 좋았다.')).toBeVisible();
    expect(screen.getByRole('button', { name: '고치기' })).toBeInTheDocument();
  });

  it('고치지 않고 그대로 저장해도 그 글을 그대로 보낸다', async () => {
    const api = mockApi({
      'GET /api/v1/me': signedIn,
      [`GET ${PATH}`]: () => json(200, draft),
      [`PUT ${PATH}`]: () => json(200, { ...confirmed, text: draft.text }),
    });
    renderRoute(`/diary/${DATE}`);

    await userEvent.click(await screen.findByRole('button', { name: '저장' }));

    await screen.findByText('일기를 저장했어요.');
    expect(api.callsTo(`PUT ${PATH}`)[0]?.body).toEqual({ text: draft.text });
  });

  it('초안을 만들지 못한 날에는 직접 써 달라고 안내하고, 빈 글은 보내지 않는다', async () => {
    const api = mockApi({
      'GET /api/v1/me': signedIn,
      [`GET ${PATH}`]: () => json(200, { ...draft, text: '' }),
    });
    renderRoute(`/diary/${DATE}`);

    expect(await screen.findByText(/초안을 만들지 못했어요/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '저장' }));

    expect(editor()).toBeInvalid();
    expect(editor()).toHaveAccessibleDescription(/일기 내용을 입력해 주세요\./);
    expect(editor()).toHaveFocus();
    expect(api.callsTo(`PUT ${PATH}`)).toHaveLength(0);
  });

  it('한 번 확인한 일기에 새 이야기가 이어 붙은 초안이면 그렇다고 알려준다', async () => {
    mockApi({
      'GET /api/v1/me': signedIn,
      [`GET ${PATH}`]: () => json(200, { ...draft, confirmed_at: '2026-09-20T09:00:00.000Z' }),
    });

    renderRoute(`/diary/${DATE}`);

    expect(await screen.findByText(/새로 나눈 이야기를 이어 붙인 초안이에요/)).toBeInTheDocument();
  });

  it('저장에 실패하면 알리고, 쓰던 글은 그대로 둔다', async () => {
    mockApi({
      'GET /api/v1/me': signedIn,
      [`GET ${PATH}`]: () => json(200, draft),
      [`PUT ${PATH}`]: networkFailure,
    });
    renderRoute(`/diary/${DATE}`);
    const user = userEvent.setup();
    await screen.findByLabelText('일기');
    await user.type(editor(), ' 더 쓴 글');

    await user.click(screen.getByRole('button', { name: '저장' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('인터넷 연결이 고르지 않아요.');
    expect(editor()).toHaveValue(`${draft.text} 더 쓴 글`);
    expect(screen.getByRole('button', { name: '저장' })).toBeEnabled();
  });

  it('서버가 글을 받지 못하겠다고 하면(공백뿐인 글) 입력란에 알린다', async () => {
    mockApi({
      'GET /api/v1/me': signedIn,
      [`GET ${PATH}`]: () => json(200, draft),
      [`PUT ${PATH}`]: () => problem(422, 'validation_failed', { fields: ['text'] }),
    });
    renderRoute(`/diary/${DATE}`);

    await userEvent.click(await screen.findByRole('button', { name: '저장' }));

    await waitFor(() => expect(editor()).toBeInvalid());
    expect(editor()).toHaveAccessibleDescription(/일기 내용을 입력해 주세요\./);
  });
});

describe('일기 열람과 수정', () => {
  it('확인한 일기는 읽는 화면으로 열고, 고치기를 눌러야 고칠 수 있다', async () => {
    mockApi({ 'GET /api/v1/me': signedIn, [`GET ${PATH}`]: () => json(200, confirmed) });
    renderRoute(`/diary/${DATE}`);
    const user = userEvent.setup();

    expect(await screen.findByText(confirmed.text)).toBeVisible();
    expect(screen.queryByLabelText('일기')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '고치기' }));
    expect(editor()).toHaveValue(confirmed.text);
    expect(screen.getByText(/대화에서 나온 마음 신호는 바뀌지 않아요/)).toBeInTheDocument();

    await user.type(editor(), ' 지울 글');
    await user.click(screen.getByRole('button', { name: '그만두기' }));
    expect(screen.queryByLabelText('일기')).not.toBeInTheDocument();
    expect(screen.getByText(confirmed.text)).toBeVisible();
  });

  it('일기가 없는 날에는 없다고 알리고, 직접 써서 저장할 수 있다', async () => {
    const api = mockApi({
      'GET /api/v1/me': signedIn,
      [`GET ${PATH}`]: () => problem(404, 'not_found'),
      [`PUT ${PATH}`]: () => json(200, { ...confirmed, text: '직접 쓴 일기' }),
    });
    renderRoute(`/diary/${DATE}`);
    const user = userEvent.setup();

    expect(
      await screen.findByRole('heading', { name: '이 날의 일기가 아직 없어요' }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '직접 쓰기' }));
    await user.type(editor(), '직접 쓴 일기');
    await user.click(screen.getByRole('button', { name: '저장' }));

    expect(await screen.findByText('일기를 저장했어요.')).toBeInTheDocument();
    expect(api.callsTo(`PUT ${PATH}`)[0]?.body).toEqual({ text: '직접 쓴 일기' });
  });

  it('대화하지 않은 날에는 일기를 쓸 수 없다고 알린다', async () => {
    mockApi({
      'GET /api/v1/me': signedIn,
      [`GET ${PATH}`]: () => problem(404, 'not_found'),
      [`PUT ${PATH}`]: () => problem(404, 'not_found'),
    });
    renderRoute(`/diary/${DATE}`);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '직접 쓰기' }));
    await user.type(editor(), '쓸 수 없는 날의 글');

    await user.click(screen.getByRole('button', { name: '저장' }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '이 날에는 나눈 대화가 없어서 일기를 쓸 수 없어요.',
    );
  });

  it('일기를 불러오지 못하면 알리고 다시 불러올 수 있게 한다', async () => {
    let online = false;
    mockApi({
      'GET /api/v1/me': signedIn,
      [`GET ${PATH}`]: () => (online ? json(200, confirmed) : networkFailure()),
    });
    renderRoute(`/diary/${DATE}`);

    expect(await screen.findByRole('alert')).toHaveTextContent('인터넷 연결이 고르지 않아요.');
    online = true;
    await userEvent.click(screen.getByRole('button', { name: '다시 불러오기' }));

    expect(await screen.findByText(confirmed.text)).toBeVisible();
  });

  it.each(['2026-02-30', 'today', '2026-9-1'])(
    '날짜 꼴이 아닌 주소(%s)로는 서버에 묻지 않고 없는 화면을 보여 준다',
    async (value) => {
      const api = mockApi({ 'GET /api/v1/me': signedIn });

      renderRoute(`/diary/${value}`);

      expect(
        await screen.findByRole('heading', { name: '페이지를 찾지 못했어요' }),
      ).toBeInTheDocument();
      expect(api.calls.filter((call) => call.path.includes('/diaries'))).toHaveLength(0);
    },
  );
});

describe('하루 지우기', () => {
  it('무엇이 함께 사라지는지 하나하나 알리고, 확인한 뒤에만 지운다', async () => {
    const api = mockApi({
      'GET /api/v1/me': signedIn,
      [`GET ${PATH}`]: () => json(200, confirmed),
      [`DELETE ${DAY_PATH}`]: noContent,
      'GET /api/v1/diaries': () => json(200, { items: [] }),
    });
    const { router } = renderRoute(`/diary/${DATE}`);
    const user = userEvent.setup();

    await user.click(await screen.findByRole('button', { name: '이 날의 기록 지우기' }));

    const confirm = screen.getByRole('alertdialog', {
      name: '9월 20일 일요일의 기록을 모두 지울까요?',
    });
    expect(within(confirm).getByRole('heading')).toHaveFocus();
    const items = within(confirm)
      .getAllByRole('listitem')
      .map((item) => item.textContent);
    expect(items).toEqual([
      '이 날의 일기',
      '이 날 나눈 대화 전체',
      '그 대화에서 읽어 낸 마음 신호와, 근거가 된 내 말',
      '이 날의 이야기에서 나온 기억',
    ]);
    expect(confirm).toHaveTextContent('지운 기록은 되돌릴 수 없어요.');
    expect(api.callsTo(`DELETE ${DAY_PATH}`)).toHaveLength(0);

    await user.click(within(confirm).getByRole('button', { name: '모두 지우기' }));

    expect(await screen.findByText('9월 20일 일요일의 기록을 지웠어요.')).toBeInTheDocument();
    expect(api.callsTo(`DELETE ${DAY_PATH}`)).toHaveLength(1);
    expect(router.state.location.pathname).toBe('/diary');
    expect(router.state.location.search).toBe('?month=2026-09');
  });

  it('지우지 않기를 고르면 아무것도 보내지 않고, 지우기 버튼으로 초점이 돌아간다', async () => {
    const api = mockApi({
      'GET /api/v1/me': signedIn,
      [`GET ${PATH}`]: () => json(200, confirmed),
    });
    renderRoute(`/diary/${DATE}`);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '이 날의 기록 지우기' }));

    await user.click(screen.getByRole('button', { name: '지우지 않기' }));

    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(api.callsTo(`DELETE ${DAY_PATH}`)).toHaveLength(0);
    await waitFor(() =>
      expect(screen.getByRole('button', { name: '이 날의 기록 지우기' })).toHaveFocus(),
    );
  });

  it('지우지 못하면 확인하는 자리에서 알리고, 기록은 그대로 보인다', async () => {
    mockApi({
      'GET /api/v1/me': signedIn,
      [`GET ${PATH}`]: () => json(200, confirmed),
      [`DELETE ${DAY_PATH}`]: () => new Response(null, { status: 502 }),
    });
    const { router } = renderRoute(`/diary/${DATE}`);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '이 날의 기록 지우기' }));

    await user.click(screen.getByRole('button', { name: '모두 지우기' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('조금 뒤에 다시 시도해 주세요.');
    expect(router.state.location.pathname).toBe(`/diary/${DATE}`);
    expect(screen.getByText(confirmed.text)).toBeVisible();
  });

  it('일기가 없는 날에도 그날의 대화를 지울 수 있게 지우기를 열어 둔다', async () => {
    mockApi({ 'GET /api/v1/me': signedIn, [`GET ${PATH}`]: () => problem(404, 'not_found') });

    renderRoute(`/diary/${DATE}`);

    expect(await screen.findByRole('button', { name: '이 날의 기록 지우기' })).toBeEnabled();
  });
});
