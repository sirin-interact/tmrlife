import createClient from 'openapi-fetch';

import {
  apiErrorFromResponse,
  isApiError,
  networkError,
  offlineError,
  unexpectedResponse,
} from '@/api/errors';
import type { paths } from '@/api/schema';

// 답이 오지 않는 연결을 언제까지 기다릴지다. 와이파이에서 이동통신으로 넘어가는 순간처럼 연결이 조용히 끊기면
// 브라우저는 운영체제의 제한 시간(1분 이상)까지 기다린다. 그동안 화면에는 "잠시만 기다려 주세요"만 떠 있게 된다.
export const REQUEST_TIMEOUT_MS = 15_000;

/**
 * 정해 둔 시간 안에 답이 오지 않으면 요청을 끊는다.
 * AbortSignal.timeout과 AbortSignal.any를 쓰지 않는 까닭: 설치해 두고 오래 쓰는 폰의 브라우저에는 아직 없는 것이 있다.
 */
async function fetchWithTimeout(request: Request): Promise<Response> {
  const controller = new AbortController();
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, REQUEST_TIMEOUT_MS);

  // 화면을 떠나서 취소된 요청은 그대로 취소되어야 한다.
  const forwardAbort = () => controller.abort();
  if (request.signal.aborted) controller.abort();
  else request.signal.addEventListener('abort', forwardAbort, { once: true });

  try {
    // 호출하는 순간의 fetch를 쓴다. 클라이언트를 만들 때의 fetch를 붙잡아 두면 테스트가 바꿔 끼울 수 없다.
    return await globalThis.fetch(request, { signal: controller.signal });
  } catch (error) {
    // 시간이 다 되어 끊은 것은 "취소"가 아니라 "연결 실패"다. 취소로 두면 조회 계층이 오류를 버려서 화면이 계속 기다린다.
    if (timedOut) throw networkError();
    throw error;
  } finally {
    clearTimeout(timer);
    request.signal.removeEventListener('abort', forwardAbort);
  }
}

export const api = createClient<paths>({
  // API는 언제나 화면과 같은 출처에 있다(개발 서버와 운영 웹 서버가 /api를 뒤로 넘긴다).
  // 주소를 설정으로 빼지 않는 까닭: 다른 출처를 가리키는 순간 세션 쿠키와 서버의 같은 출처 확인이 함께 깨진다.
  baseUrl: window.location.origin,
  // 세션 쿠키는 같은 출처 요청에만 실린다. 'include'로 넓히지 않는다.
  // 넓혀 두면 나중에 다른 출처를 부르는 코드가 생겼을 때 쿠키가 밖으로 따라 나간다.
  credentials: 'same-origin',
  headers: { Accept: 'application/json, application/problem+json' },
  fetch: fetchWithTimeout,
});

interface FetchResult<T> {
  data?: T;
  error?: unknown;
  response: Response;
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError';
}

function isOffline(): boolean {
  // 브라우저가 "연결되어 있다"고 말해도 실제로는 끊겨 있을 수 있다. "끊겼다"고 말할 때만 믿는다.
  return typeof navigator !== 'undefined' && navigator.onLine === false;
}

/**
 * 요청 하나를 보내고, 실패를 모두 ApiError로 바꿔 던진다.
 * 화면 쪽은 "성공한 본문" 아니면 "ApiError" 둘 중 하나만 받는다.
 */
export async function send<T>(
  request: () => Promise<FetchResult<T>>,
): Promise<{ data: T | undefined; response: Response }> {
  let result: FetchResult<T>;
  try {
    result = await request();
  } catch (error) {
    // 이미 이 앱의 오류로 바뀐 것(제한 시간)은 그대로 올린다.
    if (isApiError(error)) throw error;
    // 화면을 떠나서 취소된 요청은 실패가 아니다. 그대로 흘려보내면 조회 계층이 알아서 버린다.
    if (isAbortError(error)) throw error;
    // 성공 응답인데 본문이 JSON이 아닌 경우다. 공용 와이파이의 로그인 페이지가 끼어들면 이렇게 된다.
    if (error instanceof SyntaxError) throw unexpectedResponse(0);
    // fetch가 던진 오류는 붙이지 않는다. 메시지에 주소가 들어 있을 수 있고, 화면은 "연결 실패"라는 사실만 쓴다.
    throw isOffline() ? offlineError() : networkError();
  }

  if (!result.response.ok) {
    throw apiErrorFromResponse(result.response, result.error);
  }
  return { data: result.data, response: result.response };
}

/** 본문이 있어야 하는 성공 응답에서 본문을 꺼낸다. */
export function requireBody<T>(data: T | undefined, response: Response): T {
  if (data === undefined) throw unexpectedResponse(response.status);
  return data;
}
