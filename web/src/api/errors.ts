import type {
  ConsentKind,
  ConsentProblem,
  PasswordReason,
  ProblemCode,
  ProblemField,
} from '@/api/types';

/**
 * 서버가 정한 오류 코드에, 서버가 말해 줄 수 없는 네 가지를 더한다.
 * - `offline`: 기기가 인터넷에 연결되어 있지 않다고 브라우저가 알려 왔다.
 * - `network_error`: 응답을 받지 못했다(연결 끊김, 서버에 닿지 않음, 정해 둔 시간 안에 답이 없음).
 * - `unexpected_response`: 응답은 받았지만 약속한 꼴이 아니다(공용 와이파이의 로그인 페이지, 이 앱이 모르는 새 코드).
 * - `session_not_kept`: 로그인은 성공했는데 바로 다음 요청에 세션이 실리지 않았다(브라우저가 쿠키를 막고 있다).
 */
export type ApiErrorCode =
  ProblemCode | 'offline' | 'network_error' | 'unexpected_response' | 'session_not_kept';

// 값을 빠뜨리면 컴파일이 멈추도록 배열이 아니라 Record로 적는다. 명세에 코드가 늘면 여기서 먼저 드러난다.
const PROBLEM_CODES: Record<ProblemCode, true> = {
  validation_failed: true,
  invalid_credentials: true,
  unauthenticated: true,
  cross_origin_rejected: true,
  forbidden: true,
  not_found: true,
  method_not_allowed: true,
  email_taken: true,
  payload_too_large: true,
  unsupported_media_type: true,
  weak_password: true,
  consent_required: true,
  rate_limited: true,
  internal_error: true,
  service_unavailable: true,
};

const PASSWORD_REASONS: Record<PasswordReason, true> = {
  too_short: true,
  too_long: true,
  too_common: true,
  matches_email: true,
  invalid_encoding: true,
};

const PROBLEM_FIELDS: Record<ProblemField, true> = {
  email: true,
  display_name: true,
  timezone: true,
  text: true,
  q: true,
};

// 본문을 읽을 수 없을 때 상태 코드만으로 알 수 있는 경우다.
// 앞단 프록시가 직접 답하면(한도 초과, 서버 점검) 본문이 약속한 꼴이 아니다.
// 502와 504는 서버를 새 버전으로 바꾸는 동안 앞단이 빈 본문으로 내는 답이다. 새로 고쳐도 풀리지 않고 잠시 기다리면 풀린다.
const CODE_BY_STATUS: Partial<Record<number, ProblemCode>> = {
  401: 'unauthenticated',
  429: 'rate_limited',
  502: 'service_unavailable',
  503: 'service_unavailable',
  504: 'service_unavailable',
};

function codeFromStatus(status: number): ApiErrorCode {
  const known = CODE_BY_STATUS[status];
  if (known !== undefined) return known;
  // 나머지 5xx도 서버 쪽 문제다. "앱이 옛 버전일 수 있다"는 안내는 2xx와 4xx의 모르는 응답에만 맞는 말이다.
  return status >= 500 ? 'internal_error' : 'unexpected_response';
}

interface ApiErrorInit {
  code: ApiErrorCode;
  status: number;
  requestId?: string;
  reasons?: PasswordReason[];
  fields?: ProblemField[];
  consents?: ConsentProblem;
  retryAfterSeconds?: number;
}

export class ApiError extends Error {
  readonly code: ApiErrorCode;
  /** HTTP 상태 코드. 응답을 받지 못했으면 0이다. */
  readonly status: number;
  /** 문의할 때 서버 기록에서 이 요청을 찾는 데 쓴다. */
  readonly requestId: string | undefined;
  readonly reasons: readonly PasswordReason[];
  readonly fields: readonly ProblemField[];
  readonly consents: ConsentProblem | undefined;
  readonly retryAfterSeconds: number | undefined;

  constructor(init: ApiErrorInit) {
    // 메시지에는 코드만 담는다. 오류 객체는 콘솔이나 오류 화면으로 흘러갈 수 있어서
    // 요청에 담았던 값(이메일, 비밀번호)이 섞일 여지를 두지 않는다.
    super(init.code);
    this.name = 'ApiError';
    this.code = init.code;
    this.status = init.status;
    this.requestId = init.requestId;
    this.reasons = init.reasons ?? [];
    this.fields = init.fields ?? [];
    this.consents = init.consents;
    this.retryAfterSeconds = init.retryAfterSeconds;
  }
}

export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError;
}

export function networkError(): ApiError {
  return new ApiError({ code: 'network_error', status: 0 });
}

export function offlineError(): ApiError {
  return new ApiError({ code: 'offline', status: 0 });
}

export function unexpectedResponse(status: number): ApiError {
  return new ApiError({ code: 'unexpected_response', status });
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function knownValues<T extends string>(value: unknown, known: Record<T, true>): T[] {
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is T => typeof item === 'string' && Object.hasOwn(known, item));
}

function strings(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is string => typeof item === 'string');
}

function parseConsents(value: unknown): ConsentProblem | undefined {
  if (!isRecord(value)) return undefined;
  // 이 앱이 모르는 종류가 와도 버리지 않는다. 가입 화면이 규칙을 다시 받아 와서 판단한다.
  return {
    missing: strings(value.missing) as ConsentKind[],
    outdated: strings(value.outdated) as ConsentKind[],
  };
}

function parseRetryAfter(header: string | null): number | undefined {
  if (header === null || !/^\d+$/.test(header.trim())) return undefined;
  const seconds = Number.parseInt(header, 10);
  return Number.isSafeInteger(seconds) ? seconds : undefined;
}

/**
 * 실패한 응답을 ApiError로 바꾼다. body는 이미 JSON으로 읽은 본문이고, 읽지 못했으면 무엇이든 올 수 있다.
 * 서버가 이 앱보다 새 판일 수 있으므로(설치된 앱은 옛 코드를 오래 들고 있다) 모르는 값이 와도 던지지 않는다.
 */
export function apiErrorFromResponse(response: Response, body: unknown): ApiError {
  const problem = isRecord(body) ? body : {};
  const status = response.status;

  const code: ApiErrorCode =
    typeof problem.code === 'string' && Object.hasOwn(PROBLEM_CODES, problem.code)
      ? (problem.code as ProblemCode)
      : codeFromStatus(status);

  return new ApiError({
    code,
    status,
    requestId:
      typeof problem.request_id === 'string'
        ? problem.request_id
        : (response.headers.get('X-Request-Id') ?? undefined),
    reasons: knownValues(problem.reasons, PASSWORD_REASONS),
    fields: knownValues(problem.fields, PROBLEM_FIELDS),
    consents: parseConsents(problem.consents),
    retryAfterSeconds: parseRetryAfter(response.headers.get('Retry-After')),
  });
}
