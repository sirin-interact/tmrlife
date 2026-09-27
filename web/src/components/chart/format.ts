/** 그림과 표가 같은 꼴로 숫자를 적도록 한 곳에 둔다. */

/**
 * 소수점이 필요한 값만 소수로 적는다. 정수는 정수로 둔다.
 * 3이 "3.00"으로 보이면 센 값인지 계산한 값인지 헷갈리고, 자리를 두 칸 더 쓴다.
 */
export function formatDecimal(value: number, digits = 2): string {
  if (Number.isInteger(value)) return String(value);
  // 끝의 0은 떼어 낸다. 0.50은 0.5로 적는다.
  return value.toFixed(digits).replace(/\.?0+$/, '');
}

/**
 * 분수로 보여 준 값을 소수로도 덧붙인다. "7 / 8 (0.88)"
 *
 * 판정 기준이 소수(0.4 · 0.7)라서, 분수만 적어 두면 보는 사람이 나눠 봐야 판정이 맞는지 알 수 있다.
 * 나눗셈 자체는 규칙이 아니라 같은 값을 다른 꼴로 적는 일이라 화면에서 해도 된다.
 */
export function formatRatio(num: number, den: number): string {
  return `(${formatDecimal(den === 0 ? 0 : num / den)})`;
}
