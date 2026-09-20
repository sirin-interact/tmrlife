import type { AuthRequirements } from '@/api/types';

export interface PasswordCheck {
  tooShort: boolean;
  tooLong: boolean;
  matchesEmail: boolean;
}

const encoder = new TextEncoder();

/**
 * 서버가 비밀번호에 거는 규칙 가운데 화면에서 미리 볼 수 있는 것만 본다.
 * 흔한 비밀번호인지는 서버만 안다. 여기서 통과해도 서버가 거절할 수 있다.
 *
 * 세는 법을 서버와 맞춘다. 서버는 호환 정규화(NFKC)를 한 뒤에 길이는 글자 수로, 상한은 UTF-8 바이트로 센다.
 * 자모를 풀어 쓴 한글이나 전각 영문자를 그대로 세면 화면의 안내와 서버의 판단이 어긋난다.
 */
export function checkPassword(
  password: string,
  email: string,
  rules: AuthRequirements['password'],
): PasswordCheck {
  const normalized = password.normalize('NFKC');
  const folded = normalized.toLowerCase();
  const emailFolded = email.trim().toLowerCase();
  const at = emailFolded.lastIndexOf('@');
  const localPart = at > 0 ? emailFolded.slice(0, at) : '';

  return {
    tooShort: Array.from(normalized).length < rules.min_length,
    tooLong: encoder.encode(normalized).length > rules.max_bytes,
    matchesEmail: folded !== '' && (folded === emailFolded || folded === localPart),
  };
}
