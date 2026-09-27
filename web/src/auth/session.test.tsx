import type { QueryClient } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router';
import { describe, expect, it } from 'vitest';

import { ApiError } from '@/api/errors';
import { AppProviders } from '@/app/providers';
import { meQueryKey } from '@/auth/queryKeys';
import { useLogin, useLogout, useMe, useSignup } from '@/auth/session';
import { createQueryClient } from '@/lib/queryClient';
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

function setup() {
  const queryClient = createQueryClient();
  queryClient.setDefaultOptions({
    ...queryClient.getDefaultOptions(),
    queries: { ...queryClient.getDefaultOptions().queries, retryDelay: 0 },
  });
  // useLogout은 성공하면 로그인 화면으로 옮기기까지 한다. 세션을 비우는 일과 한 박자에 해야 하기 때문이다.
  // 그래서 이 고리들을 시험할 때도 경로가 있어야 한다.
  const wrapper = ({ children }: { children: ReactNode }) => (
    <AppProviders queryClient={queryClient}>
      <MemoryRouter>{children}</MemoryRouter>
    </AppProviders>
  );
  return { queryClient, wrapper };
}

const currentMe = (queryClient: QueryClient) => queryClient.getQueryData(meQueryKey);

describe('useMe', () => {
  it('로그인한 상태면 사용자 정보를 준다', async () => {
    mockApi({ 'GET /api/v1/me': signedIn });
    const { wrapper } = setup();

    const { result } = renderHook(() => useMe(), { wrapper });

    await waitFor(() => expect(result.current.data).toEqual(testMe));
  });

  it('401은 오류가 아니라 로그인하지 않은 상태(null)다', async () => {
    const api = mockApi({ 'GET /api/v1/me': signedOut });
    const { wrapper } = setup();

    const { result } = renderHook(() => useMe(), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toBeNull();
    expect(result.current.isError).toBe(false);
    // 다시 보내도 같은 답이므로 한 번만 묻는다.
    expect(api.calls).toHaveLength(1);
  });

  it('연결 실패는 한 번 더 해 본 뒤에 오류가 된다', async () => {
    const api = mockApi({ 'GET /api/v1/me': networkFailure });
    const { wrapper } = setup();

    const { result } = renderHook(() => useMe(), { wrapper });

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toMatchObject({ code: 'network_error' });
    expect(api.calls).toHaveLength(2);
  });
});

describe('useLogin', () => {
  it('로그인에 성공하면 로그인 상태가 채워진다', async () => {
    let loggedIn = false;
    mockApi({
      'GET /api/v1/me': () => (loggedIn ? json(200, testMe) : problem(401, 'unauthenticated')),
      'POST /api/v1/auth/login': () => {
        loggedIn = true;
        return json(200, { user: testUser });
      },
    });
    const { queryClient, wrapper } = setup();
    const { result } = renderHook(() => ({ me: useMe(), login: useLogin() }), { wrapper });
    await waitFor(() => expect(result.current.me.data).toBeNull());

    await act(() =>
      result.current.login.mutateAsync({ email: testUser.email, password: 'correct horse' }),
    );

    expect(currentMe(queryClient)).toEqual(testMe);
    await waitFor(() => expect(result.current.me.data).toEqual(testMe));
  });

  it('비밀번호가 틀리면 로그인 상태는 그대로이고 오류 코드가 전해진다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'POST /api/v1/auth/login': () => problem(401, 'invalid_credentials'),
    });
    const { queryClient, wrapper } = setup();
    const { result } = renderHook(() => ({ me: useMe(), login: useLogin() }), { wrapper });
    await waitFor(() => expect(result.current.me.data).toBeNull());

    await act(async () => {
      await expect(
        result.current.login.mutateAsync({ email: testUser.email, password: 'wrong' }),
      ).rejects.toMatchObject({ code: 'invalid_credentials', status: 401 });
    });

    expect(currentMe(queryClient)).toBeNull();
  });

  it('로그인은 됐는데 세션이 이어지지 않으면(쿠키 차단) 성공으로 치지 않는다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'POST /api/v1/auth/login': () => json(200, { user: testUser }),
    });
    const { queryClient, wrapper } = setup();
    const { result } = renderHook(() => useLogin(), { wrapper });

    await act(async () => {
      await expect(
        result.current.mutateAsync({ email: testUser.email, password: 'correct horse' }),
      ).rejects.toMatchObject({ code: 'session_not_kept' });
    });

    expect(currentMe(queryClient)).toBeNull();
  });
});

describe('useSignup', () => {
  it('가입에 성공하면 바로 로그인 상태가 된다', async () => {
    let signedUp = false;
    mockApi({
      'GET /api/v1/me': () => (signedUp ? json(200, testMe) : problem(401, 'unauthenticated')),
      'POST /api/v1/auth/signup': () => {
        signedUp = true;
        return json(201, { user: testUser });
      },
    });
    const { queryClient, wrapper } = setup();
    const { result } = renderHook(() => useSignup(), { wrapper });

    await act(() =>
      result.current.mutateAsync({
        email: testUser.email,
        password: 'correct horse battery',
        consents: [...testRequirements.consents],
      }),
    );

    expect(currentMe(queryClient)).toEqual(testMe);
  });
});

describe('useLogout', () => {
  it('로그아웃하면 로그인 상태를 비우고 남아 있던 조회 결과를 모두 버린다', async () => {
    mockApi({ 'GET /api/v1/me': signedIn, 'POST /api/v1/auth/logout': noContent });
    const { queryClient, wrapper } = setup();
    queryClient.setQueryData(['diary', 'list'], ['누군가의 기록']);
    const { result } = renderHook(() => ({ me: useMe(), logout: useLogout() }), { wrapper });
    await waitFor(() => expect(result.current.me.data).toEqual(testMe));

    await act(() => result.current.logout.mutateAsync());

    expect(currentMe(queryClient)).toBeNull();
    expect(queryClient.getQueryData(['diary', 'list'])).toBeUndefined();
    await waitFor(() => expect(result.current.me.data).toBeNull());
  });

  it('로그아웃에 실패하면 세션이 살아 있으므로 로그인 상태를 그대로 둔다', async () => {
    mockApi({ 'GET /api/v1/me': signedIn, 'POST /api/v1/auth/logout': networkFailure });
    const { queryClient, wrapper } = setup();
    const { result } = renderHook(() => ({ me: useMe(), logout: useLogout() }), { wrapper });
    await waitFor(() => expect(result.current.me.data).toEqual(testMe));

    await act(async () => {
      await expect(result.current.logout.mutateAsync()).rejects.toMatchObject({
        code: 'network_error',
      });
    });

    expect(currentMe(queryClient)).toEqual(testMe);
  });
});

describe('세션이 도중에 끝났을 때', () => {
  it('어느 조회든 401 unauthenticated를 받으면 로그인 상태가 비워진다', async () => {
    const { queryClient } = setup();
    queryClient.setQueryData(meQueryKey, testMe);

    await expect(
      queryClient.fetchQuery({
        queryKey: ['diary', 'list'],
        queryFn: () => Promise.reject(new ApiError({ code: 'unauthenticated', status: 401 })),
      }),
    ).rejects.toBeInstanceOf(ApiError);

    expect(currentMe(queryClient)).toBeNull();
  });

  it('로그인 실패(invalid_credentials)는 세션이 끝난 것이 아니다', async () => {
    const { queryClient } = setup();
    queryClient.setQueryData(meQueryKey, testMe);

    await expect(
      queryClient.fetchQuery({
        queryKey: ['anything'],
        queryFn: () => Promise.reject(new ApiError({ code: 'invalid_credentials', status: 401 })),
      }),
    ).rejects.toBeInstanceOf(ApiError);

    expect(currentMe(queryClient)).toEqual(testMe);
  });
});
