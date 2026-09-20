import { describe, expect, it } from 'vitest';

import { ApiError, type ApiErrorCode } from '@/api/errors';
import { describeError, formatWait } from '@/content/errorMessages';

const error = (code: ApiErrorCode, status: number, extra = {}) =>
  new ApiError({ code, status, ...extra });

describe('오류 코드를 화면 문구로 바꾸기', () => {
  it.each<[ApiErrorCode, number, string]>([
    ['invalid_credentials', 401, '이메일이나 비밀번호가 맞지 않아요. 다시 확인해 주세요.'],
    ['email_taken', 409, '이미 가입된 이메일이에요. 로그인해 주세요.'],
    [
      'cross_origin_rejected',
      403,
      '요청을 확인하지 못했어요. 화면을 새로 고친 뒤 다시 시도해 주세요.',
    ],
    ['network_error', 0, '인터넷 연결이 고르지 않아요. 연결을 확인한 뒤 다시 시도해 주세요.'],
    ['internal_error', 500, '잠시 문제가 생겼어요. 잠시 뒤에 다시 시도해 주세요.'],
    ['offline', 0, '인터넷에 연결되어 있지 않아요. 연결되면 다시 시도해 주세요.'],
    ['service_unavailable', 503, '내일이 잠시 다시 준비하고 있어요. 조금 뒤에 다시 시도해 주세요.'],
    [
      'session_not_kept',
      0,
      '로그인 상태를 이어 가지 못했어요. 브라우저에서 쿠키를 허용했는지 확인해 주세요.',
    ],
  ])('%s', (code, status, message) => {
    expect(describeError(error(code, status)).message).toBe(message);
  });

  it('이미 가입된 이메일은 이메일 칸에도 표시한다', () => {
    expect(describeError(error('email_taken', 409)).fields).toEqual({
      email: '이미 가입된 이메일이에요.',
    });
  });

  it('약한 비밀번호는 이유마다 문구를 만들어 비밀번호 칸에 붙인다', () => {
    const description = describeError(
      error('weak_password', 422, { reasons: ['too_short', 'too_common', 'matches_email'] }),
      { passwordMinLength: 10 },
    );

    expect(description.message).toBe('비밀번호를 다시 정해 주세요.');
    expect(description.fields.password).toBe(
      '비밀번호는 10자 이상이어야 해요. 많이 쓰여서 짐작하기 쉬운 비밀번호예요. 다른 비밀번호를 정해 주세요. 이메일 주소와 같은 비밀번호는 쓸 수 없어요.',
    );
  });

  it('약한 비밀번호인데 이유가 없어도 비밀번호 칸을 가리킨다', () => {
    expect(describeError(error('weak_password', 422)).fields.password).toBe(
      '다른 비밀번호를 정해 주세요.',
    );
  });

  it('빠진 동의와 판이 바뀐 동의를 다르게 안내한다', () => {
    const missing = describeError(
      error('consent_required', 422, { consents: { missing: ['privacy'], outdated: [] } }),
    );
    const outdated = describeError(
      error('consent_required', 422, { consents: { missing: [], outdated: ['terms'] } }),
    );

    expect(missing.message).toBe('필수 동의 항목을 모두 확인해 주세요.');
    expect(missing.fields.consents).toBe(missing.message);
    expect(outdated.message).toBe(
      '동의 내용이 새로 바뀌었어요. 내용을 확인하고 다시 동의해 주세요.',
    );
  });

  it('한도에 걸리면 누구 탓인지 말하지 않고 기다릴 시간을 함께 알려준다', () => {
    expect(describeError(error('rate_limited', 429, { retryAfterSeconds: 42 })).message).toBe(
      '짧은 시간에 요청이 많이 몰렸어요. 42초 뒤에 다시 시도해 주세요.',
    );
    expect(describeError(error('rate_limited', 429, { retryAfterSeconds: 61 })).message).toBe(
      '짧은 시간에 요청이 많이 몰렸어요. 2분 뒤에 다시 시도해 주세요.',
    );
    expect(describeError(error('rate_limited', 429)).message).toBe(
      '짧은 시간에 요청이 많이 몰렸어요. 잠시 뒤에 다시 시도해 주세요.',
    );
  });

  it('서버가 다시 뜨는 동안의 응답에는 새로 고침을 권하지 않는다', () => {
    const message = describeError(error('service_unavailable', 502)).message;

    expect(message).toContain('조금 뒤에 다시');
    expect(message).not.toContain('새로 고친');
  });

  it('값이 틀렸다는 응답은 서버가 알려준 칸에 문구를 붙인다', () => {
    const description = describeError(
      error('validation_failed', 422, { fields: ['email', 'display_name', 'timezone'] }),
      { displayNameMaxLength: 40 },
    );

    expect(description.message).toBe('입력한 내용을 다시 확인해 주세요.');
    expect(description.fields).toEqual({
      email: '이메일 주소를 다시 확인해 주세요.',
      displayName: '이름은 40자까지 쓸 수 있어요. 줄바꿈 같은 글자는 쓸 수 없어요.',
    });
  });

  it('어느 칸인지 모르는 400은 폼 전체에 대한 안내만 한다', () => {
    expect(describeError(error('validation_failed', 400))).toEqual({
      message: '입력한 내용을 다시 확인해 주세요.',
      fields: {},
    });
  });

  it('앱이 서버보다 옛 판일 때 생기는 오류는 새로 고침을 권한다', () => {
    for (const code of ['not_found', 'method_not_allowed', 'unexpected_response'] as const) {
      expect(describeError(error(code, 404)).message).toContain('화면을 새로 고친 뒤');
    }
  });

  it('API 오류가 아닌 것은 내용을 옮기지 않고 일반 안내를 한다', () => {
    const description = describeError(new Error('password=hunter2 leaked into a message'));

    expect(description.message).toBe('잠시 문제가 생겼어요. 잠시 뒤에 다시 시도해 주세요.');
  });
});

describe('기다릴 시간 적기', () => {
  it.each([
    [0, '1초'],
    [1, '1초'],
    [59, '59초'],
    [60, '1분'],
    [61, '2분'],
    [3599, '60분'],
    [3600, '1시간'],
    [3601, '2시간'],
  ])('%d초는 "%s"', (seconds, expected) => {
    expect(formatWait(seconds)).toBe(expected);
  });
});
