import { afterEach, describe, expect, it, vi } from 'vitest';

import { fetchAuthRequirements, fetchMe, login, logout, signup } from '@/api/auth';
import { REQUEST_TIMEOUT_MS } from '@/api/client';
import { ApiError } from '@/api/errors';
import {
  json,
  mockApi,
  networkFailure,
  noContent,
  problem,
  testMe,
  testRequirements,
  testUser,
} from '@/test/mockApi';

afterEach(() => {
  vi.useRealTimers();
});

async function caught(promise: Promise<unknown>): Promise<ApiError> {
  const error = await promise.then(
    () => undefined,
    (reason: unknown) => reason,
  );
  expect(error).toBeInstanceOf(ApiError);
  return error as ApiError;
}

describe('API 클라이언트', () => {
  it('같은 출처로만 쿠키를 싣고, 본문은 JSON으로 보낸다', async () => {
    const api = mockApi({ 'POST /api/v1/auth/login': () => json(200, { user: testUser }) });

    const user = await login({ email: 'dawn@example.com', password: 'correct horse battery' });

    expect(user).toEqual(testUser);
    expect(api.calls).toEqual([
      {
        method: 'POST',
        path: '/api/v1/auth/login',
        query: {},
        body: { email: 'dawn@example.com', password: 'correct horse battery' },
        credentials: 'same-origin',
      },
    ]);
  });

  it('요청은 화면과 같은 출처로 나간다', async () => {
    let requested = '';
    mockApi({
      'GET /api/v1/auth/requirements': (request) => {
        requested = request.url;
        return json(200, testRequirements);
      },
    });

    await expect(fetchAuthRequirements()).resolves.toEqual(testRequirements);
    expect(requested).toBe(`${window.location.origin}/api/v1/auth/requirements`);
  });

  it('본문이 없는 성공 응답(204)도 성공이다', async () => {
    mockApi({ 'POST /api/v1/auth/logout': noContent });

    await expect(logout()).resolves.toBeUndefined();
  });
});

describe('로그인한 사용자 조회', () => {
  it('로그인한 상태면 사용자와 설정을 돌려준다', async () => {
    mockApi({ 'GET /api/v1/me': () => json(200, testMe) });

    await expect(fetchMe()).resolves.toEqual(testMe);
  });

  it('401은 오류가 아니라 로그인하지 않았다는 답(null)이다', async () => {
    mockApi({ 'GET /api/v1/me': () => problem(401, 'unauthenticated') });

    await expect(fetchMe()).resolves.toBeNull();
  });

  it('그 밖의 실패는 오류로 던진다', async () => {
    mockApi({ 'GET /api/v1/me': () => problem(503, 'service_unavailable') });

    const error = await caught(fetchMe());
    expect(error.code).toBe('service_unavailable');
    expect(error.status).toBe(503);
  });
});

describe('오류 응답 읽기', () => {
  it('problem+json 본문에서 코드, 상태, 요청 ID, 이유를 읽는다', async () => {
    mockApi({
      'POST /api/v1/auth/signup': () =>
        problem(422, 'weak_password', { reasons: ['too_short', 'too_common'] }),
    });

    const error = await caught(
      signup({ email: 'dawn@example.com', password: 'short', consents: [] }),
    );

    expect(error).toMatchObject({
      code: 'weak_password',
      status: 422,
      requestId: 'req-test',
      reasons: ['too_short', 'too_common'],
    });
  });

  it('빠졌거나 판이 지난 동의를 읽는다', async () => {
    mockApi({
      'POST /api/v1/auth/signup': () =>
        problem(422, 'consent_required', {
          consents: { missing: ['privacy'], outdated: ['terms'] },
        }),
    });

    const error = await caught(signup({ email: 'a@example.com', password: 'x', consents: [] }));

    expect(error.code).toBe('consent_required');
    expect(error.consents).toEqual({ missing: ['privacy'], outdated: ['terms'] });
  });

  it('Retry-After 헤더의 초를 읽는다', async () => {
    mockApi({
      'POST /api/v1/auth/login': () => problem(429, 'rate_limited', {}, { 'Retry-After': '42' }),
    });

    const error = await caught(login({ email: 'a@example.com', password: 'x' }));

    expect(error.code).toBe('rate_limited');
    expect(error.retryAfterSeconds).toBe(42);
  });

  it('연결이 끊기면 network_error가 되고, fetch가 던진 오류의 내용은 옮기지 않는다', async () => {
    mockApi({ 'POST /api/v1/auth/login': networkFailure });

    const error = await caught(login({ email: 'a@example.com', password: 'x' }));

    expect(error.code).toBe('network_error');
    expect(error.status).toBe(0);
    expect(error.message).toBe('network_error');
    expect(error.cause).toBeUndefined();
  });

  it('브라우저가 연결이 끊겼다고 알려 온 상태에서 실패하면 offline이다', async () => {
    vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(false);
    mockApi({ 'POST /api/v1/auth/login': networkFailure });

    const error = await caught(login({ email: 'a@example.com', password: 'x' }));

    expect(error.code).toBe('offline');
    expect(error.status).toBe(0);
  });

  it('정해 둔 시간 안에 답이 없으면 요청을 끊고 network_error로 알린다', async () => {
    vi.useFakeTimers();
    let aborted = false;
    // 답하지 않는 서버. 요청이 끊기면 브라우저의 fetch처럼 AbortError로 거절한다.
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (_input: RequestInfo | URL, init?: RequestInit) =>
          new Promise<Response>((_resolve, reject) => {
            init?.signal?.addEventListener('abort', () => {
              aborted = true;
              reject(new DOMException('The operation was aborted.', 'AbortError'));
            });
          }),
      ),
    );

    const pending = caught(login({ email: 'a@example.com', password: 'x' }));
    await vi.advanceTimersByTimeAsync(REQUEST_TIMEOUT_MS - 1);
    expect(aborted).toBe(false);
    await vi.advanceTimersByTimeAsync(1);

    expect(aborted).toBe(true);
    expect((await pending).code).toBe('network_error');
  });

  it('화면을 떠나서 취소된 요청은 오류로 바꾸지 않고 취소 그대로 둔다', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (_input: RequestInfo | URL, init?: RequestInit) =>
          new Promise<Response>((_resolve, reject) => {
            init?.signal?.addEventListener('abort', () =>
              reject(new DOMException('The operation was aborted.', 'AbortError')),
            );
          }),
      ),
    );
    const controller = new AbortController();

    const pending = fetchMe(controller.signal);
    controller.abort();

    await expect(pending).rejects.toMatchObject({ name: 'AbortError' });
  });

  it.each([502, 503, 504])(
    '앞단 프록시가 본문 없이 %d로 답하면 "잠시 뒤에 다시"에 해당하는 service_unavailable이다',
    async (status) => {
      // 서버를 새 버전으로 바꾸는 동안 앞단이 내는 답이다. 본문도 Content-Type도 없다.
      mockApi({ 'POST /api/v1/auth/login': () => new Response(null, { status }) });

      const error = await caught(login({ email: 'a@example.com', password: 'x' }));

      expect(error.code).toBe('service_unavailable');
      expect(error.status).toBe(status);
    },
  );

  it('앞단 프록시가 HTML로 답하면 상태 코드로 짐작한다', async () => {
    const html = (status: number) => () =>
      new Response('<html>proxy error page</html>', {
        status,
        headers: { 'Content-Type': 'text/html' },
      });
    const codeFor = async (status: number) => {
      mockApi({ 'POST /api/v1/auth/login': html(status) });
      return (await caught(login({ email: 'a@example.com', password: 'x' }))).code;
    };

    expect(await codeFor(502)).toBe('service_unavailable');
    expect(await codeFor(429)).toBe('rate_limited');
    // 나머지 5xx는 서버 쪽 문제다. 새로 고침을 권하는 unexpected_response로 두지 않는다.
    expect(await codeFor(500)).toBe('internal_error');
    expect(await codeFor(520)).toBe('internal_error');
    // 모르는 4xx는 앱이 서버보다 옛 판일 때 생긴다.
    expect(await codeFor(410)).toBe('unexpected_response');
  });

  it('이 앱이 모르는 코드가 와도 던지지 않고 unexpected_response로 다룬다', async () => {
    mockApi({
      'POST /api/v1/auth/login': () =>
        new Response(JSON.stringify({ code: 'brand_new_code', status: 418 }), {
          status: 418,
          headers: { 'Content-Type': 'application/problem+json' },
        }),
    });

    const error = await caught(login({ email: 'a@example.com', password: 'x' }));

    expect(error.code).toBe('unexpected_response');
    expect(error.status).toBe(418);
  });

  it('성공 응답인데 본문이 JSON이 아니면 unexpected_response다', async () => {
    mockApi({
      'GET /api/v1/auth/requirements': () =>
        new Response('<html>Wi-Fi login</html>', {
          status: 200,
          headers: { 'Content-Type': 'text/html' },
        }),
    });

    expect((await caught(fetchAuthRequirements())).code).toBe('unexpected_response');
  });

  it('오류 객체 어디에도 보낸 값이 들어가지 않는다', async () => {
    mockApi({ 'POST /api/v1/auth/login': () => problem(401, 'invalid_credentials') });

    const error = await caught(
      login({ email: 'secret-address@example.com', password: 'secret-password-value' }),
    );

    const dumped = JSON.stringify({ ...error, message: error.message, stack: error.stack });
    expect(dumped).not.toContain('secret-address');
    expect(dumped).not.toContain('secret-password-value');
  });
});

describe('가입 요청', () => {
  const body = {
    email: 'dawn@example.com',
    password: 'correct horse battery',
    timezone: 'Mars/Olympus_Mons',
    consents: [...testRequirements.consents],
  };

  it('서버가 시간대만 받지 못하면 시간대를 빼고 한 번 더 보낸다', async () => {
    let attempts = 0;
    const api = mockApi({
      'POST /api/v1/auth/signup': () => {
        attempts += 1;
        return attempts === 1
          ? problem(422, 'validation_failed', { fields: ['timezone'] })
          : json(201, { user: testUser });
      },
    });

    await expect(signup(body)).resolves.toEqual(testUser);

    const [first, second] = api.calls.map((call) => call.body as Record<string, unknown>);
    expect(first).toHaveProperty('timezone', 'Mars/Olympus_Mons');
    expect(second).not.toHaveProperty('timezone');
    expect(second).toMatchObject({ email: body.email, consents: body.consents });
  });

  it('사용자가 고칠 수 있는 칸이 함께 틀렸으면 다시 보내지 않고 알린다', async () => {
    const api = mockApi({
      'POST /api/v1/auth/signup': () =>
        problem(422, 'validation_failed', { fields: ['email', 'timezone'] }),
    });

    const error = await caught(signup(body));

    expect(error.fields).toEqual(['email', 'timezone']);
    expect(api.calls).toHaveLength(1);
  });
});
