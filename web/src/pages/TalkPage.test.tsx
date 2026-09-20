import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  aiText,
  installFakeWebSocket,
  ready,
  RECORD_DATE,
  testResources,
  thinking,
  utterance,
  type FakeSocket,
  type FakeSockets,
} from '@/test/fakeSocket';
import { json, mockApi, problem, signedIn } from '@/test/mockApi';
import { renderRoute } from '@/test/renderRoute';
import { CLOSE_CODE } from '@/talk/messages';

const GREETING = '오늘 하루는 어떠셨어요?';

afterEach(() => {
  vi.useRealTimers();
});

async function connected(sockets: FakeSockets): Promise<FakeSocket> {
  await waitFor(() => expect(sockets.all.length).toBeGreaterThan(0));
  return sockets.latest();
}

/** 대화 화면을 열고 새 대화의 첫 안부까지 받은 상태 */
async function openTalk(routes: Parameters<typeof mockApi>[0] = {}) {
  const api = mockApi({ 'GET /api/v1/me': signedIn, ...routes });
  const sockets = installFakeWebSocket();
  const view = renderRoute('/talk');
  const socket = await connected(sockets);
  act(() => {
    socket.open();
    socket.receive(ready());
    socket.receive(aiText(0, GREETING));
  });
  await screen.findByText(GREETING);
  return { api, sockets, socket, ...view };
}

const input = () => screen.getByLabelText('하고 싶은 이야기');
const log = () => screen.getByRole('list', { name: '대화 내용' });

function lastUserText(socket: FakeSocket) {
  const frame = socket.sentOfType('user_text').at(-1);
  if (!frame) throw new Error('보낸 글이 없다');
  return frame as { client_message_id: string; text: string };
}

describe('대화 화면: 연결', () => {
  it('화면과 같은 출처의 대화 채널에 연결하고, 준비되는 동안 알린다', async () => {
    mockApi({ 'GET /api/v1/me': signedIn });
    const sockets = installFakeWebSocket();
    renderRoute('/talk');

    const socket = await connected(sockets);

    expect(socket.url).toBe(`ws://${window.location.host}/ws/v1/conversation`);
    expect(screen.getByRole('status')).toHaveTextContent('대화를 준비하고 있어요.');
    expect(document.title).toBe('이야기하기 · 내일');
    // 준비되기 전에도 글을 미리 써 둘 수는 있다. 보내는 것만 막는다.
    expect(input()).toBeEnabled();
    expect(screen.getByRole('button', { name: '보내기' })).toBeDisabled();
  });

  it('첫 안부를 보여 주고 기록 날짜를 적는다. 방금 도착한 말은 화면 낭독기에도 읽어 준다', async () => {
    await openTalk();

    expect(within(log()).getByText(GREETING)).toBeInTheDocument();
    expect(screen.getByText('9월 20일 일요일')).toHaveAttribute('datetime', RECORD_DATE);
    const live = document.querySelector('[aria-live="polite"]');
    expect(live).toHaveTextContent(`내일: ${GREETING}`);
  });

  it('이어가는 대화의 지난 발화는 보여 주되, 화면 낭독기에 한꺼번에 읽어 주지는 않는다', async () => {
    mockApi({ 'GET /api/v1/me': signedIn });
    const sockets = installFakeWebSocket();
    renderRoute('/talk');
    const socket = await connected(sockets);

    act(() => {
      socket.open();
      socket.receive(
        ready({
          resumed: true,
          utterances: [
            utterance(0, 'ai', GREETING),
            utterance(
              1,
              'user',
              '오늘 친구랑 놀러갔다왔어',
              '11111111-1111-4111-8111-111111111111',
            ),
            utterance(2, 'ai', '오 재밌게 놀고 오셨어요?'),
          ],
        }),
      );
    });

    const items = await within(log()).findAllByRole('listitem');
    expect(items.map((item) => item.textContent)).toEqual([
      `내일: ${GREETING}`,
      '나: 오늘 친구랑 놀러갔다왔어',
      '내일: 오 재밌게 놀고 오셨어요?',
    ]);
    expect(document.querySelector('[aria-live="polite"]')).toBeEmptyDOMElement();
  });

  it('연결이 끊기면 알리고, 지난 대화는 그대로 두고, 보내기를 막는다', async () => {
    const { socket } = await openTalk();
    await userEvent.type(input(), '쓰던 글');

    act(() => socket.drop());

    expect(screen.getByRole('status')).toHaveTextContent('연결이 잠시 끊겼어요. 다시 잇고 있어요.');
    expect(within(log()).getByText(GREETING)).toBeInTheDocument();
    expect(input()).toHaveValue('쓰던 글');
    expect(screen.getByRole('button', { name: '보내기' })).toBeDisabled();
  });

  it('다른 화면이 대화를 이어받으면 그렇게 알리고, 여기서 이어갈지 사용자가 고른다', async () => {
    const { socket, sockets } = await openTalk();

    act(() => socket.drop(CLOSE_CODE.takenOver));

    expect(screen.getByRole('status')).toHaveTextContent('다른 화면에서 이야기를 이어가고 있어요.');
    expect(sockets.all).toHaveLength(1);

    await userEvent.click(screen.getByRole('button', { name: '여기서 이어가기' }));
    expect(sockets.all).toHaveLength(2);
  });

  it('연결이 열리지도 못하고 닫히면 로그인 상태를 다시 확인하고, 세션이 끝났으면 로그인 화면으로 간다', async () => {
    let signedOutNow = false;
    mockApi({
      'GET /api/v1/me': (request) =>
        signedOutNow ? problem(401, 'unauthenticated') : signedIn(request),
    });
    const sockets = installFakeWebSocket();
    const { router } = renderRoute('/talk');
    const socket = await connected(sockets);

    signedOutNow = true;
    act(() => socket.drop());

    expect(await screen.findByRole('heading', { level: 2, name: '로그인' })).toBeInTheDocument();
    // 로그인하면 대화 화면으로 돌아온다.
    expect(router.state.location.state).toEqual({ from: '/talk' });
  });

  it('대화 화면에서는 메뉴 줄을 접지만 "도움이 필요할 때"는 그대로 있다', async () => {
    await openTalk();

    expect(screen.queryByRole('navigation', { name: '주요 메뉴' })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: '도움이 필요할 때' })).toHaveAttribute('href', '/help');
  });
});

describe('대화 화면: 글 쓰기', () => {
  it('Enter로 보내고, 답을 준비하는 동안 표시를 보여 주고, 답이 오면 표시를 거둔다', async () => {
    const { socket } = await openTalk();
    const user = userEvent.setup();

    await user.type(input(), '오늘 친구랑 놀러갔다왔어{Enter}');

    const sent = lastUserText(socket);
    expect(sent.text).toBe('오늘 친구랑 놀러갔다왔어');
    expect(sent.client_message_id).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/,
    );
    expect(input()).toHaveValue('');
    expect(input()).toHaveFocus();
    expect(within(log()).getByText('오늘 친구랑 놀러갔다왔어')).toBeInTheDocument();
    expect(within(log()).getByRole('status')).toHaveTextContent('내일이 답을 준비하고 있어요');

    act(() => {
      socket.receive(thinking(sent.client_message_id, 1));
      socket.receive(aiText(2, '오 재밌게 놀고 오셨어요? 어디서 놀았어요?'));
    });

    expect(within(log()).queryByRole('status')).not.toBeInTheDocument();
    expect(
      within(log()).getByText('오 재밌게 놀고 오셨어요? 어디서 놀았어요?'),
    ).toBeInTheDocument();
  });

  it('Shift+Enter는 보내지 않고 줄을 바꾼다', async () => {
    const { socket } = await openTalk();
    const user = userEvent.setup();

    await user.type(input(), '첫 줄{Shift>}{Enter}{/Shift}둘째 줄');

    expect(socket.sentOfType('user_text')).toHaveLength(0);
    expect(input()).toHaveValue('첫 줄\n둘째 줄');
  });

  it('한글을 조합하는 중의 Enter로는 보내지 않는다', async () => {
    const { socket } = await openTalk();
    await userEvent.type(input(), '안녕하세');

    // 크롬: 조합 중인 글자를 확정하는 Enter는 isComposing이 참이다.
    fireEvent.keyDown(input(), { key: 'Enter', isComposing: true });
    // 사파리: 조합이 끝난 직후의 Enter는 isComposing이 거짓이지만 keyCode가 229다.
    fireEvent.keyDown(input(), { key: 'Enter', keyCode: 229 });
    expect(socket.sentOfType('user_text')).toHaveLength(0);

    // 조합이 끝난 뒤의 Enter에는 보낸다.
    fireEvent.keyDown(input(), { key: 'Enter', keyCode: 13 });
    expect(lastUserText(socket).text).toBe('안녕하세');
  });

  it('보내기 버튼으로도 보내고, 빈 글은 보내지 않는다', async () => {
    const { socket } = await openTalk();
    const user = userEvent.setup();
    const send = screen.getByRole('button', { name: '보내기' });

    expect(send).toBeDisabled();
    await user.type(input(), '   ');
    expect(send).toBeDisabled();

    await user.type(input(), '별일은 없었어');
    await user.click(send);

    expect(lastUserText(socket).text).toBe('별일은 없었어');
    expect(input()).toHaveFocus();
  });

  it('답을 기다리는 동안에는 다음 글을 쓸 수는 있어도 보내지는 않는다', async () => {
    const { socket } = await openTalk();
    const user = userEvent.setup();
    await user.type(input(), '첫 번째{Enter}');

    await user.type(input(), '두 번째{Enter}');

    expect(socket.sentOfType('user_text')).toHaveLength(1);
    expect(input()).toHaveValue('두 번째');
  });

  it('너무 긴 글은 보내지 않고 알린다', async () => {
    const { socket } = await openTalk();

    fireEvent.change(input(), { target: { value: '가'.repeat(2001) } });

    expect(screen.getByRole('alert')).toHaveTextContent('한 번에 2,000자까지 보낼 수 있어요.');
    expect(input()).toBeInvalid();
    expect(screen.getByRole('button', { name: '보내기' })).toBeDisabled();
    fireEvent.keyDown(input(), { key: 'Enter', keyCode: 13 });
    expect(socket.sentOfType('user_text')).toHaveLength(0);
  });

  it('서버가 글을 처리하지 못하면 그 글에 다시 보내기를 붙이고, 누르면 같은 식별자로 다시 보낸다', async () => {
    const { socket } = await openTalk();
    const user = userEvent.setup();
    await user.type(input(), '처리하다 실패한 글{Enter}');
    const first = lastUserText(socket);

    act(() =>
      socket.receive({
        type: 'error',
        code: 'internal_error',
        client_message_id: first.client_message_id,
      }),
    );

    expect(within(log()).getByText('보내지 못했어요.')).toBeInTheDocument();
    await user.click(within(log()).getByRole('button', { name: '다시 보내기' }));

    expect(socket.sentOfType('user_text')).toHaveLength(2);
    expect(lastUserText(socket)).toEqual(first);
    expect(within(log()).queryByText('보내지 못했어요.')).not.toBeInTheDocument();
  });

  it('오류 메시지가 와도 사용자가 쓴 글이나 서버의 코드를 그대로 화면에 옮기지 않는다', async () => {
    const { socket } = await openTalk();

    act(() => socket.receive({ type: 'error', code: 'unsupported_mode' }));

    expect(screen.getByRole('status')).toHaveTextContent(
      '문제가 생겼어요. 화면을 새로 고친 뒤 다시 시도해 주세요.',
    );
    expect(screen.queryByText(/unsupported_mode/)).not.toBeInTheDocument();
  });
});

describe('대화 화면: 도움 자원', () => {
  it('resources를 받으면 번호를 화면에 고정하고, 누르면 바로 전화가 걸리게 한다', async () => {
    const { socket } = await openTalk();

    act(() => socket.receive({ type: 'resources', items: testResources }));

    const panel = screen.getByRole('region', { name: '지금 바로 이야기할 수 있는 곳' });
    const calls = within(panel).getAllByRole('link');
    expect(calls.map((link) => link.getAttribute('href'))).toEqual([
      'tel:109',
      'tel:15770199',
      'tel:119',
    ]);
    expect(calls[1]).toHaveAccessibleName('정신건강위기상담 1577-0199 전화하기');
    expect(document.querySelector('[aria-live="polite"]')).toHaveTextContent(
      '전화번호를 화면 위쪽에 띄워 두었어요.',
    );
  });

  it('고정한 번호는 대화가 이어지는 동안에도, 끝난 뒤에도 남아 있다', async () => {
    const { socket } = await openTalk();
    const user = userEvent.setup();
    act(() => socket.receive({ type: 'resources', items: testResources }));

    await user.type(input(), '그래도 이야기하니까 좀 낫다{Enter}');
    act(() => {
      socket.receive(thinking(lastUserText(socket).client_message_id, 2));
      socket.receive(aiText(3, '계속 듣고 있어요.'));
    });
    expect(screen.getByRole('link', { name: /109 전화하기/ })).toBeInTheDocument();

    act(() =>
      socket.receive({
        type: 'ended',
        reason: 'user',
        record_date: RECORD_DATE,
        diary_expected: false,
      }),
    );
    expect(screen.getByRole('link', { name: /109 전화하기/ })).toBeInTheDocument();
  });
});

describe('대화 화면: 끝내기', () => {
  it('끝내기는 늘 보이고, 한 번 더 확인한 뒤에 end를 보낸다', async () => {
    const { socket } = await openTalk();
    const user = userEvent.setup();

    await user.click(screen.getByRole('button', { name: '끝내기' }));

    const confirm = screen.getByRole('alertdialog', { name: '대화를 마칠까요?' });
    expect(within(confirm).getByRole('heading', { name: '대화를 마칠까요?' })).toHaveFocus();
    expect(socket.sentOfType('end')).toHaveLength(0);

    await user.click(within(confirm).getByRole('button', { name: '네, 끝낼게요' }));

    expect(socket.sentOfType('end')).toEqual([{ type: 'end' }]);
    expect(screen.getByRole('status')).toHaveTextContent('대화를 마치고 있어요.');
    expect(screen.getByRole('button', { name: '보내기' })).toBeDisabled();
  });

  it('확인에서 계속 이야기하기를 고르면 아무것도 보내지 않고 끝내기 버튼으로 초점이 돌아간다', async () => {
    const { socket } = await openTalk();
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: '끝내기' }));

    await user.click(screen.getByRole('button', { name: '계속 이야기할게요' }));

    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(socket.sentOfType('end')).toHaveLength(0);
    await waitFor(() => expect(screen.getByRole('button', { name: '끝내기' })).toHaveFocus());
  });

  it('연결이 끊긴 동안에도 끝내기는 누를 수 있다', async () => {
    const { socket } = await openTalk();
    act(() => socket.drop());

    expect(screen.getByRole('button', { name: '끝내기' })).toBeEnabled();
  });

  it('끝나면 일기 초안을 기다리고, diary_ready가 오면 그날의 일기 확인 화면으로 간다', async () => {
    const { socket, router } = await openTalk({
      [`GET /api/v1/diaries/${RECORD_DATE}`]: () => problem(404, 'not_found'),
    });
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: '끝내기' }));
    await user.click(screen.getByRole('button', { name: '네, 끝낼게요' }));

    act(() =>
      socket.receive({
        type: 'ended',
        reason: 'user',
        record_date: RECORD_DATE,
        diary_expected: true,
      }),
    );

    expect(screen.getByRole('heading', { name: '대화를 마쳤어요.' })).toHaveFocus();
    expect(screen.getByText(/일기로 옮기고 있어요/)).toBeInTheDocument();
    expect(screen.queryByLabelText('하고 싶은 이야기')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '끝내기' })).not.toBeInTheDocument();

    act(() => socket.receive({ type: 'diary_ready', record_date: RECORD_DATE }));

    await waitFor(() => expect(router.state.location.pathname).toBe(`/diary/${RECORD_DATE}`));
    expect(router.state.location.state).toEqual({ fromTalk: true });
  });

  it('기다리는 동안 읽은 "그날의 일기 없음"을 일기 화면이 그대로 쓰지 않는다', async () => {
    const draftText = '오늘은 친구랑 놀러 갔다 왔다.';
    let draftExists = false;
    const { socket, router, api } = await openTalk({
      [`GET /api/v1/diaries/${RECORD_DATE}`]: () =>
        draftExists
          ? json(200, {
              date: RECORD_DATE,
              status: 'draft',
              text: draftText,
              confirmed_at: null,
              updated_at: '2026-09-20T12:00:05.000Z',
            })
          : problem(404, 'not_found'),
    });

    act(() =>
      socket.receive({
        type: 'ended',
        reason: 'user',
        record_date: RECORD_DATE,
        diary_expected: true,
      }),
    );
    // 기다리는 자리가 초안이 왔는지 한 번 읽어 본다. 이때는 아직 없다.
    await waitFor(() => expect(api.callsTo(`GET /api/v1/diaries/${RECORD_DATE}`)).toHaveLength(1));

    draftExists = true;
    act(() => socket.receive({ type: 'diary_ready', record_date: RECORD_DATE }));

    await waitFor(() => expect(router.state.location.pathname).toBe(`/diary/${RECORD_DATE}`));
    // 옮겨 간 화면이 조금 전의 "없음"을 그대로 쓰면 초안 대신 "일기가 아직 없어요"가 보인다.
    expect(await screen.findByLabelText('일기')).toHaveValue(draftText);
  });

  it('연결이 먼저 끊겨 소식을 듣지 못해도, 일기를 직접 읽어 보다가 초안이 생기면 넘어간다', async () => {
    let draftExists = false;
    const { socket, router, api } = await openTalk({
      [`GET /api/v1/diaries/${RECORD_DATE}`]: () =>
        draftExists
          ? json(200, {
              date: RECORD_DATE,
              status: 'draft',
              text: '오늘은 친구랑 놀러 갔다 왔다.',
              confirmed_at: null,
              updated_at: '2026-09-20T12:00:05.000Z',
            })
          : problem(404, 'not_found'),
    });
    vi.useFakeTimers({ shouldAdvanceTime: true });

    act(() => {
      socket.receive({
        type: 'ended',
        reason: 'idle',
        record_date: RECORD_DATE,
        diary_expected: true,
      });
      socket.drop();
    });
    expect(
      screen.getByRole('heading', { name: '한동안 말이 없어서 대화를 마쳤어요.' }),
    ).toBeVisible();
    await waitFor(() => expect(api.callsTo(`GET /api/v1/diaries/${RECORD_DATE}`)).toHaveLength(1));

    draftExists = true;
    await act(() => vi.advanceTimersByTimeAsync(3_000));

    await waitFor(() => expect(router.state.location.pathname).toBe(`/diary/${RECORD_DATE}`));
  });

  it('초안을 만들지 않는 대화였으면 기다리지 않고 갈 곳을 보여 준다. 다시 이야기하기는 새 연결을 연다', async () => {
    const { socket, sockets } = await openTalk();
    const user = userEvent.setup();

    act(() =>
      socket.receive({
        type: 'ended',
        reason: 'user',
        record_date: RECORD_DATE,
        diary_expected: false,
      }),
    );

    expect(screen.getByText('오늘 이야기는 여기까지예요. 와 주셔서 고마워요.')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '처음으로' })).toHaveAttribute('href', '/');
    expect(screen.getByRole('link', { name: '일기장 보기' })).toHaveAttribute('href', '/diary');

    await user.click(screen.getByRole('button', { name: '다시 이야기하기' }));

    await waitFor(() => expect(sockets.all).toHaveLength(2));
    expect(screen.getByLabelText('하고 싶은 이야기')).toBeInTheDocument();
  });
});
