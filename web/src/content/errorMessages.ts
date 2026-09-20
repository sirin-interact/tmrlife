import { isApiError, type ApiError, type ApiErrorCode } from '@/api/errors';
import type { PasswordReason } from '@/api/types';

/**
 * 서버의 오류 코드를 화면 문구로 바꾸는 곳은 이 파일 하나다.
 * 서버가 보내는 영어 제목(title)은 개발자용이라 화면에 쓰지 않는다.
 */

/** 문구가 붙을 수 있는 폼의 자리 */
export type FormFieldName = 'email' | 'password' | 'displayName' | 'consents';

export interface ErrorDescription {
  /** 폼 전체에 대한 안내. 언제나 있다. */
  message: string;
  /** 특정 입력란에 붙일 안내. 그 입력란을 고치면 풀리는 오류일 때만 있다. */
  fields: Partial<Record<FormFieldName, string>>;
}

/** 화면이 알고 있는 규칙 값. 문구에 숫자를 넣을 때 쓴다. */
export interface ErrorContext {
  passwordMinLength?: number;
  displayNameMaxLength?: number;
}

const GENERIC = '잠시 문제가 생겼어요. 잠시 뒤에 다시 시도해 주세요.';
// 없는 경로, 받지 않는 메서드, 모르는 응답은 설치된 앱이 서버보다 옛 판일 때 생긴다.
const OUTDATED_APP = '앱이 최신 버전이 아닐 수 있어요. 화면을 새로 고친 뒤 다시 시도해 주세요.';

export const FORM_INVALID_MESSAGE = '입력한 내용을 확인해 주세요.';

/** 기기가 인터넷에 연결되어 있지 않을 때의 안내. 화면 위쪽의 알림과 요청 실패 안내가 같은 말을 쓴다. */
export const OFFLINE_MESSAGE = '인터넷에 연결되어 있지 않아요. 연결되면 다시 시도해 주세요.';

export function passwordReasonMessage(reason: PasswordReason, minLength?: number): string {
  switch (reason) {
    case 'too_short':
      return minLength === undefined
        ? '비밀번호가 너무 짧아요.'
        : `비밀번호는 ${minLength}자 이상이어야 해요.`;
    case 'too_long':
      return '비밀번호가 너무 길어요. 조금 줄여 주세요.';
    case 'too_common':
      return '많이 쓰여서 짐작하기 쉬운 비밀번호예요. 다른 비밀번호를 정해 주세요.';
    case 'matches_email':
      return '이메일 주소와 같은 비밀번호는 쓸 수 없어요.';
    case 'invalid_encoding':
      return '비밀번호에 쓸 수 없는 글자가 들어 있어요.';
  }
}

export function displayNameTooLongMessage(maxLength?: number): string {
  return maxLength === undefined
    ? '이름이 너무 길어요. 조금 줄여 주세요.'
    : `이름은 ${maxLength}자까지 쓸 수 있어요.`;
}

/** 기다릴 시간을 읽기 편한 단위로 올림해서 적는다. 실제보다 짧게 말하면 다시 시도했을 때 또 막힌다. */
export function formatWait(seconds: number): string {
  if (seconds < 60) return `${Math.max(1, Math.ceil(seconds))}초`;
  if (seconds < 3600) return `${Math.ceil(seconds / 60)}분`;
  return `${Math.ceil(seconds / 3600)}시간`;
}

function only(message: string): ErrorDescription {
  return { message, fields: {} };
}

type Describe = (error: ApiError, context: ErrorContext) => ErrorDescription;

// Record라서 코드 하나라도 빠지면 컴파일이 멈춘다. 명세에 코드가 늘면 문구도 함께 정해야 한다.
const DESCRIBE: Record<ApiErrorCode, Describe> = {
  offline: () => only(OFFLINE_MESSAGE),
  network_error: () => only('인터넷 연결이 고르지 않아요. 연결을 확인한 뒤 다시 시도해 주세요.'),
  unexpected_response: () => only(OUTDATED_APP),
  session_not_kept: () =>
    only('로그인 상태를 이어 가지 못했어요. 브라우저에서 쿠키를 허용했는지 확인해 주세요.'),

  invalid_credentials: () => only('이메일이나 비밀번호가 맞지 않아요. 다시 확인해 주세요.'),
  email_taken: () => ({
    message: '이미 가입된 이메일이에요. 로그인해 주세요.',
    fields: { email: '이미 가입된 이메일이에요.' },
  }),
  weak_password: (error, context) => {
    const reasons = error.reasons.map((reason) =>
      passwordReasonMessage(reason, context.passwordMinLength),
    );
    return {
      message: '비밀번호를 다시 정해 주세요.',
      fields: { password: reasons.length > 0 ? reasons.join(' ') : '다른 비밀번호를 정해 주세요.' },
    };
  },
  consent_required: (error) => {
    const outdated = (error.consents?.outdated.length ?? 0) > 0;
    const message = outdated
      ? '동의 내용이 새로 바뀌었어요. 내용을 확인하고 다시 동의해 주세요.'
      : '필수 동의 항목을 모두 확인해 주세요.';
    return { message, fields: { consents: message } };
  },
  // 한도는 같은 와이파이를 쓰는 사람들이 함께 나눠 쓰기도 한다. 이 사람이 많이 시도했다고 단정하지 않는다.
  rate_limited: (error) =>
    only(
      error.retryAfterSeconds === undefined
        ? '짧은 시간에 요청이 많이 몰렸어요. 잠시 뒤에 다시 시도해 주세요.'
        : `짧은 시간에 요청이 많이 몰렸어요. ${formatWait(error.retryAfterSeconds)} 뒤에 다시 시도해 주세요.`,
    ),
  validation_failed: (error, context) => {
    const fields: ErrorDescription['fields'] = {};
    if (error.fields.includes('email')) fields.email = '이메일 주소를 다시 확인해 주세요.';
    if (error.fields.includes('display_name')) {
      fields.displayName = `${displayNameTooLongMessage(context.displayNameMaxLength)} 줄바꿈 같은 글자는 쓸 수 없어요.`;
    }
    return { message: '입력한 내용을 다시 확인해 주세요.', fields };
  },
  cross_origin_rejected: () =>
    only('요청을 확인하지 못했어요. 화면을 새로 고친 뒤 다시 시도해 주세요.'),

  unauthenticated: () => only('로그인이 필요해요. 다시 로그인해 주세요.'),
  forbidden: () => only('지금 계정으로는 할 수 없는 일이에요.'),
  payload_too_large: () => only('보내는 내용이 너무 커요. 내용을 줄여서 다시 시도해 주세요.'),
  not_found: () => only(OUTDATED_APP),
  method_not_allowed: () => only(OUTDATED_APP),
  unsupported_media_type: () => only(OUTDATED_APP),
  internal_error: () => only(GENERIC),
  // 서버를 새 버전으로 바꾸는 동안에도 이 코드가 온다. 새로 고침이 아니라 잠시 기다리는 것이 답이다.
  service_unavailable: () =>
    only('내일이 잠시 다시 준비하고 있어요. 조금 뒤에 다시 시도해 주세요.'),
};

export function describeError(error: unknown, context: ErrorContext = {}): ErrorDescription {
  // ApiError가 아닌 것은 이 앱의 버그다. 내용을 화면에 옮기지 않는다. 무엇이 들어 있을지 알 수 없다.
  if (!isApiError(error)) return only(GENERIC);
  return DESCRIBE[error.code](error, context);
}
