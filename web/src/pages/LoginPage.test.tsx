import { onlineManager } from '@tanstack/react-query';
import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import {
  json,
  mockApi,
  networkFailure,
  problem,
  signedOut,
  testMe,
  testRequirements,
  testUser,
} from '@/test/mockApi';
import { renderRoute } from '@/test/renderRoute';

const PASSWORD = 'correct horse battery';

async function fillAndSubmit(email = testUser.email, password = PASSWORD) {
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText('이메일'), email);
  await user.type(screen.getByLabelText('비밀번호'), password);
  await user.click(screen.getByRole('button', { name: '로그인' }));
}

describe('LoginPage', () => {
  it('/login 주소에서 로그인 화면을 그린다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut });

    renderRoute('/login');

    expect(await screen.findByRole('heading', { level: 2, name: '로그인' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '로그인' })).toBeEnabled();
  });

  it('이메일과 비밀번호 입력란에 라벨이 연결되어 있고, 비밀번호 관리 앱이 알아볼 수 있다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut });

    renderRoute('/login');

    const email = await screen.findByLabelText('이메일');
    expect(email).toHaveAttribute('type', 'email');
    expect(email).toHaveAttribute('autocomplete', 'username');
    expect(screen.getByLabelText('비밀번호')).toHaveAttribute('type', 'password');
    expect(screen.getByLabelText('비밀번호')).toHaveAttribute('autocomplete', 'current-password');
  });

  it('비밀번호를 보이게 했다가 다시 가릴 수 있다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut });
    renderRoute('/login');
    const user = userEvent.setup();

    const password = await screen.findByLabelText('비밀번호');
    await user.type(password, PASSWORD);
    const toggle = screen.getByRole('button', { name: '비밀번호 보기', pressed: false });

    await user.click(toggle);
    expect(password).toHaveAttribute('type', 'text');
    expect(toggle).toHaveAttribute('aria-pressed', 'true');
    expect(password).toHaveValue(PASSWORD);

    await user.click(toggle);
    expect(password).toHaveAttribute('type', 'password');
  });

  it('비워 둔 채 제출하면 요청을 보내지 않고, 칸마다 오류를 잇고 첫 칸으로 초점을 옮긴다', async () => {
    const api = mockApi({ 'GET /api/v1/me': signedOut });
    renderRoute('/login');

    await userEvent.click(await screen.findByRole('button', { name: '로그인' }));

    const summary = await screen.findByRole('alert');
    expect(summary).toHaveTextContent('입력한 내용을 확인해 주세요.');
    expect(within(summary).getByText('이메일을 입력해 주세요.')).toBeInTheDocument();
    expect(within(summary).getByText('비밀번호를 입력해 주세요.')).toBeInTheDocument();

    const email = screen.getByLabelText('이메일');
    expect(email).toBeInvalid();
    expect(email).toHaveAccessibleDescription('이메일을 입력해 주세요.');
    expect(email).toHaveFocus();
    expect(screen.getByLabelText('비밀번호')).toHaveAccessibleDescription(
      '비밀번호를 입력해 주세요.',
    );
    expect(api.callsTo('POST /api/v1/auth/login')).toHaveLength(0);
  });

  it('이메일 꼴이 아니면 알려준다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut });
    renderRoute('/login');

    await fillAndSubmit('not-an-email');

    expect(await screen.findByLabelText('이메일')).toHaveAccessibleDescription(
      '이메일 주소를 다시 확인해 주세요.',
    );
  });

  it('오류를 고치면 그 칸의 표시가 사라진다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut });
    renderRoute('/login');
    const user = userEvent.setup();

    await user.click(await screen.findByRole('button', { name: '로그인' }));
    const email = screen.getByLabelText('이메일');
    await waitFor(() => expect(email).toBeInvalid());

    await user.type(email, testUser.email);

    await waitFor(() => expect(email).toBeValid());
    expect(email).not.toHaveAttribute('aria-describedby');
  });

  it('로그인에 성공하면 처음 화면으로 가고, 이메일은 앞뒤 공백을 뗀 채로 보낸다', async () => {
    let loggedIn = false;
    const api = mockApi({
      'GET /api/v1/me': () => (loggedIn ? json(200, testMe) : problem(401, 'unauthenticated')),
      'POST /api/v1/auth/login': () => {
        loggedIn = true;
        return json(200, { user: testUser });
      },
    });
    const { router } = renderRoute('/login');

    await fillAndSubmit(`  ${testUser.email} `);

    expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent(
      '새벽님, 안녕하세요',
    );
    expect(router.state.location.pathname).toBe('/');
    expect(api.callsTo('POST /api/v1/auth/login')[0]?.body).toEqual({
      email: testUser.email,
      password: PASSWORD,
    });
  });

  it('비밀번호가 틀리면 한국어로 알리고, 비밀번호 칸으로 초점을 옮기고, 다시 제출할 수 있게 한다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'POST /api/v1/auth/login': () => problem(401, 'invalid_credentials'),
    });
    renderRoute('/login');

    await fillAndSubmit();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('이메일이나 비밀번호가 맞지 않아요. 다시 확인해 주세요.');
    // 서버가 보낸 개발자용 영어 문구는 화면에 나오지 않는다.
    expect(alert).not.toHaveTextContent(/[A-Za-z]{4,}/);
    expect(screen.getByLabelText('비밀번호')).toHaveFocus();
    expect(screen.getByRole('button', { name: '로그인' })).toBeEnabled();
  });

  it('요청을 보내는 동안에는 제출 버튼이 꺼져 있다', async () => {
    let respond: (response: Response) => void = () => undefined;
    const api = mockApi({
      'GET /api/v1/me': signedOut,
      'POST /api/v1/auth/login': () =>
        new Promise<Response>((resolve) => {
          respond = resolve;
        }),
    });
    renderRoute('/login');

    await fillAndSubmit();

    const pending = await screen.findByRole('button', { name: '로그인하고 있어요' });
    expect(pending).toBeDisabled();
    expect(pending.closest('form')).toHaveAttribute('aria-busy', 'true');

    respond(problem(401, 'invalid_credentials'));

    expect(await screen.findByRole('button', { name: '로그인' })).toBeEnabled();
    expect(api.callsTo('POST /api/v1/auth/login')).toHaveLength(1);
  });

  it('시도 한도에 걸리면 기다릴 시간을 알려준다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'POST /api/v1/auth/login': () => problem(429, 'rate_limited', {}, { 'Retry-After': '90' }),
    });
    renderRoute('/login');

    await fillAndSubmit();

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '짧은 시간에 요청이 많이 몰렸어요. 2분 뒤에 다시 시도해 주세요.',
    );
  });

  it('연결이 끊겼으면 연결을 확인해 달라고 알린다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut, 'POST /api/v1/auth/login': networkFailure });
    renderRoute('/login');

    await fillAndSubmit();

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '인터넷 연결이 고르지 않아요. 연결을 확인한 뒤 다시 시도해 주세요.',
    );
  });

  it('기기가 인터넷에서 끊겼다고 브라우저가 알려 온 뒤에도, 요청을 붙잡아 두지 않고 바로 알린다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut, 'POST /api/v1/auth/login': networkFailure });
    renderRoute('/login');
    await screen.findByLabelText('이메일');
    vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(false);
    act(() => void window.dispatchEvent(new Event('offline')));

    try {
      await fillAndSubmit();

      // 붙잡아 두면 버튼이 "로그인하고 있어요"에 머문 채 아무 안내도 나오지 않는다.
      expect(await screen.findByRole('alert')).toHaveTextContent(
        '인터넷에 연결되어 있지 않아요. 연결되면 다시 시도해 주세요.',
      );
      expect(screen.getByRole('button', { name: '로그인' })).toBeEnabled();
    } finally {
      // 조회 계층의 연결 상태는 테스트끼리 함께 쓰는 값이다. 다음 테스트를 위해 되돌려 놓는다.
      onlineManager.setOnline(true);
    }
  });

  it('앞단이 본문 없이 502로 답하면 새로 고침이 아니라 잠시 뒤에 다시 해 보라고 알린다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'POST /api/v1/auth/login': () => new Response(null, { status: 502 }),
    });
    renderRoute('/login');

    await fillAndSubmit();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(
      '내일이 잠시 다시 준비하고 있어요. 조금 뒤에 다시 시도해 주세요.',
    );
    expect(alert).not.toHaveTextContent('새로 고친');
  });

  it('다른 출처의 요청으로 거절되면 새로 고침을 권한다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'POST /api/v1/auth/login': () => problem(403, 'cross_origin_rejected'),
    });
    renderRoute('/login');

    await fillAndSubmit();

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '요청을 확인하지 못했어요. 화면을 새로 고친 뒤 다시 시도해 주세요.',
    );
  });

  it('로그인은 됐는데 세션이 이어지지 않으면 쿠키 설정을 확인해 달라고 알린다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'POST /api/v1/auth/login': () => json(200, { user: testUser }),
    });
    renderRoute('/login');

    await fillAndSubmit();

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '로그인 상태를 이어 가지 못했어요. 브라우저에서 쿠키를 허용했는지 확인해 주세요.',
    );
  });

  it('이메일과 비밀번호를 콘솔에 남기지 않는다', async () => {
    const methods = ['log', 'info', 'warn', 'error', 'debug'] as const;
    const spies = methods.map((method) => vi.spyOn(console, method).mockImplementation(() => {}));
    mockApi({
      'GET /api/v1/me': signedOut,
      'POST /api/v1/auth/login': () => problem(401, 'invalid_credentials'),
    });
    renderRoute('/login');

    await fillAndSubmit('private-address@example.com', 'private-password-value');
    await screen.findByRole('alert');

    // 오류 객체는 JSON으로 바꾸면 빈 값이 된다. 메시지와 스택까지 글로 펴서 본다.
    const printed = spies
      .flatMap((spy) => (spy.mock.calls as unknown[][]).flat())
      .map((value: unknown) =>
        value instanceof Error ? `${value.message}\n${value.stack}` : JSON.stringify(value),
      )
      .join('\n');
    expect(printed).not.toContain('private-address');
    expect(printed).not.toContain('private-password-value');
  });

  it('가입 화면으로 가는 링크가 있다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': () => json(200, testRequirements),
    });
    renderRoute('/login');

    await userEvent.click(await screen.findByRole('link', { name: '가입하기' }));

    expect(await screen.findByRole('heading', { level: 2, name: '가입하기' })).toBeInTheDocument();
  });
});
