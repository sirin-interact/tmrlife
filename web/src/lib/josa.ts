/**
 * 숫자 뒤에 붙는 조사를 고른다.
 *
 * 숫자를 우리말로 읽었을 때 끝소리에 받침이 있는지로 갈린다. 4는 "사"라서 "4를", 1은 "일"이라서 "1을"이다.
 * 마지막 자리만 보면 되는 까닭: 십·백·천·만은 모두 받침이 있고 0("영")도 받침이 있어서, 10도 100도 "을"이다.
 * 소수는 마지막 자리 숫자로 읽으므로(4.5는 "사점오") 같은 규칙이 그대로 통한다.
 */
const HAS_FINAL_CONSONANT: Record<string, boolean> = {
  '0': true, // 영
  '1': true, // 일
  '2': false, // 이
  '3': true, // 삼
  '4': false, // 사
  '5': false, // 오
  '6': true, // 육
  '7': true, // 칠
  '8': true, // 팔
  '9': false, // 구
};

function lastDigit(value: number): string | undefined {
  const digits = String(value).replace(/[^0-9]/g, '');
  return digits.slice(-1) || undefined;
}

/** "4를", "1을" */
export function eul(value: number): string {
  const digit = lastDigit(value);
  // 읽을 자리가 없는 값(NaN 같은)에는 받침 없는 쪽을 쓴다. 문장이 어색해질 뿐 뜻은 남는다.
  return digit !== undefined && HAS_FINAL_CONSONANT[digit] === true ? '을' : '를';
}
