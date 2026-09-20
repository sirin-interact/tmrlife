import { describe, expect, it } from 'vitest';

import { checkPassword } from '@/auth/passwordPolicy';

const rules = { min_length: 10, max_bytes: 128 };

describe('비밀번호 규칙 미리 보기', () => {
  it('길이는 바이트가 아니라 글자 수로 센다', () => {
    // 한글 아홉 글자는 27바이트지만 아홉 글자다.
    expect(checkPassword('가나다라마바사아자', '', rules).tooShort).toBe(true);
    expect(checkPassword('가나다라마바사아자차', '', rules).tooShort).toBe(false);
  });

  it('이모지처럼 코드 두 개로 적히는 글자도 한 글자로 센다', () => {
    expect(checkPassword('🌅'.repeat(9), '', rules).tooShort).toBe(true);
    expect(checkPassword('🌅'.repeat(10), '', rules).tooShort).toBe(false);
  });

  it('자모를 풀어 쓴 한글은 서버처럼 합친 뒤에 센다', () => {
    const decomposed = '가나다라마바사아자차'.normalize('NFD');
    expect(Array.from(decomposed).length).toBeGreaterThan(10);

    const nine = '가나다라마바사아자'.normalize('NFD');
    expect(checkPassword(nine, '', rules).tooShort).toBe(true);
    expect(checkPassword(decomposed, '', rules).tooShort).toBe(false);
  });

  it('상한은 UTF-8 바이트로 센다', () => {
    expect(checkPassword('a'.repeat(128), '', rules).tooLong).toBe(false);
    expect(checkPassword('a'.repeat(129), '', rules).tooLong).toBe(true);
    // 한글 43글자는 129바이트다.
    expect(checkPassword('가'.repeat(42), '', rules).tooLong).toBe(false);
    expect(checkPassword('가'.repeat(43), '', rules).tooLong).toBe(true);
  });

  it('이메일 주소나 그 앞부분과 같으면 알려준다. 대소문자는 가리지 않는다', () => {
    const email = ' Dawn.Light@Example.com ';
    expect(checkPassword('dawn.light@example.com', email, rules).matchesEmail).toBe(true);
    expect(checkPassword('DAWN.LIGHT', email, rules).matchesEmail).toBe(true);
    expect(checkPassword('dawn.light.2026', email, rules).matchesEmail).toBe(false);
  });

  it('아무것도 입력하지 않았으면 이메일과 같다고 하지 않는다', () => {
    expect(checkPassword('', '', rules).matchesEmail).toBe(false);
    expect(checkPassword('', '@example.com', rules).matchesEmail).toBe(false);
  });

  it('규칙의 숫자는 서버가 준 값을 따른다', () => {
    const strict = { min_length: 14, max_bytes: 20 };
    expect(checkPassword('twelve-chars', '', strict).tooShort).toBe(true);
    expect(checkPassword('a'.repeat(21), '', strict).tooLong).toBe(true);
  });
});
