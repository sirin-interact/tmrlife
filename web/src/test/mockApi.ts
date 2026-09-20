import { expect, onTestFinished, vi } from 'vitest';

import type { Me, Problem, ProblemCode, User } from '@/api/types';

type Handler = (request: Request) => Response | Promise<Response>;
type RouteKey = `${'GET' | 'POST' | 'PUT' | 'DELETE'} /${string}`;

export interface RecordedCall {
  method: string;
  path: string;
  /** 주소의 ? 뒤에 붙은 값들 */
  query: Record<string, string>;
  /** JSON으로 읽은 요청 본문. 본문이 없으면 undefined다. */
  body: unknown;
  credentials: RequestCredentials;
}

/**
 * fetch를 대역으로 바꿔 끼우고, 경로마다 정해 둔 응답을 돌려준다.
 * 정해 두지 않은 요청이 나가면 테스트가 끝날 때 실패한다. 화면이 몰래 다른 곳을 부르는 일을 놓치지 않는다.
 */
export function mockApi(routes: Partial<Record<RouteKey, Handler>>) {
  const calls: RecordedCall[] = [];
  const unhandled: string[] = [];

  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = input instanceof Request ? input : new Request(input, init);
    const url = new URL(request.url);
    const path = url.pathname;
    const key = `${request.method} ${path}` as RouteKey;

    const text = await request.clone().text();
    calls.push({
      method: request.method,
      path,
      query: Object.fromEntries(url.searchParams),
      body: text === '' ? undefined : (JSON.parse(text) as unknown),
      credentials: request.credentials,
    });

    const handler = routes[key];
    if (!handler) {
      unhandled.push(key);
      return problem(500, 'internal_error');
    }
    return handler(request);
  });

  vi.stubGlobal('fetch', fetchMock);
  onTestFinished(() => {
    expect(unhandled, '응답을 정해 두지 않은 요청').toEqual([]);
  });

  return {
    calls,
    callsTo: (key: RouteKey) => calls.filter((call) => `${call.method} ${call.path}` === key),
  };
}

export function json(status: number, body: unknown, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
}

export function noContent(): Response {
  return new Response(null, { status: 204 });
}

export function problem(
  status: number,
  code: ProblemCode,
  extra: Partial<Pick<Problem, 'reasons' | 'consents' | 'fields'>> = {},
  headers: Record<string, string> = {},
): Response {
  const body: Problem = {
    type: `/problems/${code}`,
    title: 'Developer-facing title that must never reach the screen',
    status,
    code,
    request_id: 'req-test',
    ...extra,
  };
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/problem+json', 'X-Request-Id': 'req-test', ...headers },
  });
}

/** 연결이 끊겼을 때 브라우저의 fetch가 하는 것과 같은 실패. */
export function networkFailure(): Promise<Response> {
  return Promise.reject(new TypeError('Failed to fetch'));
}

export const testUser: User = {
  id: '0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b',
  email: 'dawn@example.com',
  display_name: '새벽',
  timezone: 'Asia/Seoul',
  is_demo: false,
  created_at: '2026-09-20T12:00:00.000Z',
};

export const testMe: Me = {
  user: testUser,
  settings: {
    reminder_enabled: true,
    reminder_time: '20:00',
    default_mode: 'voice',
    analysis_enabled: true,
    memory_enabled: true,
    mood_pick_enabled: true,
  },
};

export const testRequirements = {
  password: { min_length: 10, max_bytes: 128 },
  consents: [
    { kind: 'terms', version: '2026-09-20' },
    { kind: 'privacy', version: '2026-09-20' },
    { kind: 'sensitive_data', version: '2026-09-20' },
    { kind: 'overseas_transfer', version: '2026-09-20' },
  ],
  display_name_max_length: 40,
} as const;

export const signedOut: Handler = () => problem(401, 'unauthenticated');
export const signedIn: Handler = () => json(200, testMe);
