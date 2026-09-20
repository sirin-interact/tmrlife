import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { RouteObject } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import { GuestOnly, RequireAuth } from '@/auth/guards';
import { meQueryKey } from '@/auth/queryKeys';
import { AppShell } from '@/components/AppShell';
import { LoginPage } from '@/pages/LoginPage';
import {
  json,
  mockApi,
  networkFailure,
  noContent,
  problem,
  signedIn,
  signedOut,
  testMe,
  testRequirements,
  testUser,
} from '@/test/mockApi';
import { renderRoute } from '@/test/renderRoute';

// "가려던 곳으로 돌아간다"만 확인하려고, 같은 보호 장치에 서버를 부르지 않는 화면을 건 경로 정의를 쓴다.
const routesWithDiary: RouteObject[] = [
  {
    element: <AppShell />,
    children: [
      {
        element: <RequireAuth />,
        children: [
          { index: true, element: <h1>처음 화면</h1> },
          { path: 'diary/:date', element: <h1>그날의 일기</h1> },
        ],
      },
      { element: <GuestOnly />, children: [{ path: 'login', element: <LoginPage /> }] },
    ],
  },
];

/** 로그인에 성공하면 그 뒤로는 로그인한 상태로 답하는 서버. */
function serverWithLogin() {
  let loggedIn = false;
  return mockApi({
    'GET /api/v1/me': () => (loggedIn ? json(200, testMe) : problem(401, 'unauthenticated')),
    'POST /api/v1/auth/login': () => {
      loggedIn = true;
      return json(200, { user: testUser });
    },
  });
}

async function logIn() {
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText('이메일'), testUser.email);
  await user.type(screen.getByLabelText('비밀번호'), 'correct horse battery');
  await user.click(screen.getByRole('button', { name: '로그인' }));
}

describe('경로 보호', () => {
  it('로그인하지 않고 처음 화면에 오면 로그인 화면으로 간다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut });

    const { router } = renderRoute('/');

    expect(await screen.findByRole('heading', { level: 2, name: '로그인' })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe('/login');
  });

  it('로그인 여부를 아직 모를 때는 로그인 화면도 본문도 보여 주지 않는다', () => {
    mockApi({ 'GET /api/v1/me': () => new Promise<Response>(() => undefined) });

    renderRoute('/');

    expect(screen.getByRole('status')).toHaveTextContent('잠시만 기다려 주세요.');
    expect(screen.queryByRole('heading', { name: '로그인' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: '오늘 이야기하기' })).not.toBeInTheDocument();
  });

  it('로그인하면 가려던 화면으로 돌아간다', async () => {
    serverWithLogin();
    const { router } = renderRoute('/diary/2026-09-20?from=reminder', routesWithDiary);

    await screen.findByRole('heading', { level: 2, name: '로그인' });
    expect(router.state.location.pathname).toBe('/login');

    await logIn();

    expect(await screen.findByRole('heading', { name: '그날의 일기' })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe('/diary/2026-09-20');
    expect(router.state.location.search).toBe('?from=reminder');
  });

  it('곧바로 로그인 화면에 와서 로그인하면 처음 화면으로 간다', async () => {
    serverWithLogin();
    const { router } = renderRoute('/login', routesWithDiary);

    await logIn();

    expect(await screen.findByRole('heading', { name: '처음 화면' })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe('/');
  });

  it.each(['/login', '/signup'])('로그인한 사람이 %s에 오면 처음 화면으로 간다', async (path) => {
    mockApi({ 'GET /api/v1/me': signedIn });

    const { router } = renderRoute(path);

    expect(await screen.findByRole('link', { name: '오늘 이야기하기' })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe('/');
  });

  it('로그인 여부를 확인하지 못하면 로그인 화면으로 보내지 않고 다시 시도하게 한다', async () => {
    let online = false;
    mockApi({ 'GET /api/v1/me': () => (online ? json(200, testMe) : networkFailure()) });
    const { router } = renderRoute('/');

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '인터넷 연결이 고르지 않아요. 연결을 확인한 뒤 다시 시도해 주세요.',
    );
    expect(router.state.location.pathname).toBe('/');

    online = true;
    await userEvent.click(screen.getByRole('button', { name: '다시 시도하기' }));

    expect(await screen.findByRole('link', { name: '오늘 이야기하기' })).toBeInTheDocument();
  });

  it('첫 확인이 실패한 뒤에 로그인 상태를 다시 확인해도, 쓰던 로그인 폼은 지워지지 않는다', async () => {
    let release: (response: Response) => void = () => undefined;
    let failing = true;
    mockApi({
      'GET /api/v1/me': () =>
        failing
          ? networkFailure()
          : new Promise<Response>((resolve) => {
              release = resolve;
            }),
    });
    const { queryClient } = renderRoute('/login');
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('이메일'), testUser.email);
    await user.type(screen.getByLabelText('비밀번호'), 'correct horse battery');

    // 앱으로 돌아오거나 연결이 돌아오면 조회가 다시 나간다. 답이 올 때까지 조회는 "기다리는 중"으로 되돌아간다.
    failing = false;
    let refetch: Promise<void> = Promise.resolve();
    act(() => {
      refetch = queryClient.refetchQueries({ queryKey: meQueryKey });
    });
    await waitFor(() =>
      expect(queryClient.getQueryState(meQueryKey)?.fetchStatus).toBe('fetching'),
    );

    expect(screen.getByLabelText('이메일')).toHaveValue(testUser.email);
    expect(screen.getByLabelText('비밀번호')).toHaveValue('correct horse battery');
    expect(screen.queryByText('잠시만 기다려 주세요.')).not.toBeInTheDocument();

    await act(async () => {
      release(problem(401, 'unauthenticated'));
      await refetch;
    });
    expect(screen.getByLabelText('이메일')).toHaveValue(testUser.email);
  });

  it('로그인은 됐는데 그 뒤의 확인이 실패하면, 폼을 지우지 않고 까닭을 알린다', async () => {
    mockApi({
      'GET /api/v1/me': networkFailure,
      'POST /api/v1/auth/login': () => json(200, { user: testUser }),
    });
    const { router } = renderRoute('/login');

    await logIn();

    expect(await screen.findByRole('alert')).toHaveTextContent('인터넷 연결이 고르지 않아요.');
    expect(router.state.location.pathname).toBe('/login');
    expect(screen.getByLabelText('이메일')).toHaveValue(testUser.email);
  });

  it('없는 주소에서는 로그인하지 않았어도 안내 화면을 보여 준다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut });

    renderRoute('/no-such-page');

    expect(
      await screen.findByRole('heading', { level: 1, name: '페이지를 찾지 못했어요' }),
    ).toBeInTheDocument();
  });
});

describe('앱 틀', () => {
  it('모든 화면에 본문으로 건너뛰는 링크와 main 영역이 있다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut });

    renderRoute('/login');

    expect(screen.getByRole('link', { name: '본문으로 건너뛰기' })).toHaveAttribute(
      'href',
      '#main',
    );
    expect(screen.getByRole('main')).toHaveAttribute('id', 'main');
    await screen.findByRole('heading', { level: 2, name: '로그인' });
  });

  it('로그인하지 않았으면 로그아웃 버튼이 없다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut });

    renderRoute('/login');

    await screen.findByRole('heading', { level: 2, name: '로그인' });
    expect(screen.queryByRole('button', { name: '로그아웃' })).not.toBeInTheDocument();
  });

  it('로그아웃하면 로그인 화면으로 가고, 보던 주소는 다음 사람에게 넘기지 않는다', async () => {
    const api = mockApi({ 'GET /api/v1/me': signedIn, 'POST /api/v1/auth/logout': noContent });
    const { router } = renderRoute('/diary/2026-09-20', routesWithDiary);

    await userEvent.click(await screen.findByRole('button', { name: '로그아웃' }));

    expect(await screen.findByRole('heading', { level: 2, name: '로그인' })).toBeInTheDocument();
    expect(api.callsTo('POST /api/v1/auth/logout')).toHaveLength(1);
    await waitFor(() => expect(router.state.location.state).toBeNull());
    expect(router.state.location.pathname).toBe('/login');
    expect(screen.queryByRole('button', { name: '로그아웃' })).not.toBeInTheDocument();
  });

  it('로그아웃에 실패하면 알리고 로그인한 상태로 둔다', async () => {
    mockApi({ 'GET /api/v1/me': signedIn, 'POST /api/v1/auth/logout': networkFailure });
    renderRoute('/');

    await userEvent.click(await screen.findByRole('button', { name: '로그아웃' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('인터넷 연결이 고르지 않아요.');
    expect(screen.getByRole('button', { name: '로그아웃' })).toBeEnabled();
    expect(screen.getByRole('link', { name: '오늘 이야기하기' })).toBeInTheDocument();
  });

  it('화면이 바뀌면 초점을 본문으로 옮긴다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': () => json(200, testRequirements),
    });
    renderRoute('/login');

    await userEvent.click(await screen.findByRole('link', { name: '가입하기' }));

    await screen.findByRole('heading', { level: 2, name: '가입하기' });
    expect(screen.getByRole('main')).toHaveFocus();
  });
});

describe('어느 화면에서나', () => {
  it.each(['/login', '/signup', '/help', '/legal/terms', '/no-such-page'])(
    '%s: 로그인하지 않았어도 "도움이 필요할 때"로 가는 링크가 있다',
    async (path) => {
      mockApi({
        'GET /api/v1/me': signedOut,
        'GET /api/v1/auth/requirements': () => json(200, testRequirements),
        'GET /api/v1/resources': () => json(200, { items: [] }),
      });

      renderRoute(path);

      const link = await screen.findByRole('link', { name: '도움이 필요할 때' });
      expect(link).toHaveAttribute('href', '/help');
    },
  );

  it('로그인한 사람에게는 오늘과 일기장으로 가는 메뉴가 있다', async () => {
    mockApi({ 'GET /api/v1/me': signedIn });

    renderRoute('/');

    const menu = await screen.findByRole('navigation', { name: '주요 메뉴' });
    expect(menu).toContainElement(screen.getByRole('link', { name: '오늘' }));
    expect(screen.getByRole('link', { name: '일기장' })).toHaveAttribute('href', '/diary');
    expect(screen.getByRole('link', { name: '오늘' })).toHaveAttribute('aria-current', 'page');
  });

  it('기기가 인터넷에서 끊기면 알리고, 돌아오면 알림을 거둔다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut });
    renderRoute('/login');
    await screen.findByRole('heading', { level: 2, name: '로그인' });
    const onLine = vi.spyOn(navigator, 'onLine', 'get');

    onLine.mockReturnValue(false);
    act(() => void window.dispatchEvent(new Event('offline')));
    expect(screen.getByRole('status')).toHaveTextContent('인터넷에 연결되어 있지 않아요.');

    onLine.mockReturnValue(true);
    act(() => void window.dispatchEvent(new Event('online')));
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });
});

describe('화면마다의 제목', () => {
  it.each([
    ['/login', '로그인 · 내일'],
    ['/signup', '가입하기 · 내일'],
    ['/help', '도움이 필요할 때 · 내일'],
    ['/legal/privacy', '개인정보 처리방침 · 내일'],
    ['/no-such-page', '페이지를 찾지 못했어요 · 내일'],
  ])('%s의 제목은 "%s"', async (path, title) => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': () => json(200, testRequirements),
      'GET /api/v1/resources': () => json(200, { items: [] }),
    });

    renderRoute(path);

    await waitFor(() => expect(document.title).toBe(title));
  });

  it('화면을 옮기면 제목도 바뀐다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/auth/requirements': () => json(200, testRequirements),
    });
    renderRoute('/login');
    await waitFor(() => expect(document.title).toBe('로그인 · 내일'));

    await userEvent.click(await screen.findByRole('link', { name: '가입하기' }));

    await waitFor(() => expect(document.title).toBe('가입하기 · 내일'));
  });
});
