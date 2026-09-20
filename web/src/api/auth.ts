import { api, requireBody, send } from '@/api/client';
import { isApiError } from '@/api/errors';
import type { AuthRequirements, LoginRequest, Me, SignupRequest, User } from '@/api/types';

export async function fetchAuthRequirements(signal?: AbortSignal): Promise<AuthRequirements> {
  const { data, response } = await send(() => api.GET('/api/v1/auth/requirements', { signal }));
  return requireBody(data, response);
}

/** 로그인한 사용자를 돌려준다. 로그인하지 않았으면 null이다. */
export async function fetchMe(signal?: AbortSignal): Promise<Me | null> {
  try {
    const { data, response } = await send(() => api.GET('/api/v1/me', { signal }));
    return requireBody(data, response);
  } catch (error) {
    // 401은 실패가 아니라 "로그인하지 않았다"는 답이다. 오류로 두면 화면마다 이 경우를 따로 걸러야 한다.
    if (isApiError(error) && error.status === 401) return null;
    throw error;
  }
}

async function postSignup(body: SignupRequest): Promise<User> {
  const { data, response } = await send(() => api.POST('/api/v1/auth/signup', { body }));
  return requireBody(data, response).user;
}

export async function signup(body: SignupRequest): Promise<User> {
  try {
    return await postSignup(body);
  } catch (error) {
    // 시간대는 브라우저가 알려준 값이라 사용자가 고칠 수 없다. 브라우저와 서버가 아는 시간대 목록은 판이 달라서
    // 드물게 서버가 모르는 이름이 나온다. 그때는 시간대를 빼고 한 번 더 보낸다. 서버가 기본 시간대를 쓴다.
    const onlyTimezoneRejected =
      isApiError(error) &&
      error.code === 'validation_failed' &&
      error.fields.length === 1 &&
      error.fields[0] === 'timezone';
    if (body.timezone === undefined || !onlyTimezoneRejected) throw error;

    const { timezone: _timezone, ...withoutTimezone } = body;
    return postSignup(withoutTimezone);
  }
}

export async function login(body: LoginRequest): Promise<User> {
  const { data, response } = await send(() => api.POST('/api/v1/auth/login', { body }));
  return requireBody(data, response).user;
}

export async function logout(): Promise<void> {
  await send(() => api.POST('/api/v1/auth/logout'));
}
