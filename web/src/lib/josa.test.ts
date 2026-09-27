import { describe, expect, it } from 'vitest';

import { eul } from '@/lib/josa';

describe('숫자 뒤의 조사', () => {
  it.each([
    [0, '을'], // 영
    [1, '을'], // 일
    [2, '를'], // 이
    [3, '을'], // 삼
    [4, '를'], // 사
    [5, '를'], // 오
    [6, '을'], // 육
    [7, '을'], // 칠
    [8, '을'], // 팔
    [9, '를'], // 구
  ])('%i 뒤에는 "%s"가 온다', (value, expected) => {
    expect(eul(value)).toBe(expected);
  });

  it('두 자리 이상은 마지막 자리로 고른다. 십·백·천은 모두 받침이 있어서 10도 "10을"이다', () => {
    expect(eul(10)).toBe('을');
    expect(eul(14)).toBe('를');
    expect(eul(16)).toBe('을');
    expect(eul(100)).toBe('을');
    expect(eul(24)).toBe('를');
  });

  it('소수는 마지막 자리 숫자로 읽는다. 4.5는 "사점오"라서 "4.5를"이다', () => {
    expect(eul(4.5)).toBe('를');
    expect(eul(4.1)).toBe('을');
  });
});
