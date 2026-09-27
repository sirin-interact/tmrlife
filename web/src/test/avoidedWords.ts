/**
 * 화면 문구에 쓰지 않기로 한 말들. 이 앱은 마음 상태를 관찰하고 기록하는 도구이지 의료 서비스가 아니다.
 *
 * 문구를 담은 파일마다 같은 목록을 다시 적지 않도록 한 곳에 둔다. 새 화면의 문구도 이 목록으로 검사한다.
 */
export const AVOIDED_WORDS = [
  '우울',
  '진단',
  '판정',
  '검사',
  '위험도',
  '환자',
  '증상',
  '치료',
  '상담',
] as const;

/**
 * "이것이 아니다"라고 밝히는 문장에서만 허락하는 말.
 *
 * 이 앱이 검사나 진단이 아니라는 사실은 화면에서 먼저 말해야 하는 것이라, 그 말을 부정하는 문장에서는 그 낱말을 쓴다.
 * 허락은 아래 문장에만 준다. 새 문장이 같은 낱말을 쓰려면 이 목록에 그 문장을 적어야 한다.
 */
export const DENIAL_SENTENCES: readonly string[] = [
  '대화에서 읽어 낸 이야기예요. 검사나 진단의 결과가 아니에요.',
  '이 화면의 숫자는 대화 기록에서 추정한 값이에요. 정식 자가 점검이나 검사의 결과가 아니고, 진단도 아니에요.',
  '추정 점수와 개입 단계를 숫자로 보여 주지 않는 까닭이에요. 힘든 사람에게 점수와 경고는 도움보다 상처가 되기 쉽고, 이 값은 검사 결과가 아니라 대화 기록에서 추정한 값이에요.',
];

/** 부정하는 문장에서만 쓸 수 있는 낱말. '우울'과 '위험도'는 어디에서도 쓰지 않는다. */
export const DENIABLE_WORDS: readonly string[] = ['검사', '진단'];

/**
 * 객체 안의 문구를 모두 모은다.
 *
 * 숫자를 끼워 넣는 문구는 함수로 적혀 있어서, 그 함수의 코드를 글자로 읽어 검사한다.
 * 함수를 불러 보려면 인자를 지어내야 하고, 그러면 검사하지 못하는 문구가 생긴다.
 */
export function stringsIn(value: unknown): string[] {
  if (typeof value === 'string') return [value];
  if (typeof value === 'function') return [String(value)];
  if (Array.isArray(value)) return value.flatMap(stringsIn);
  if (typeof value === 'object' && value !== null) return Object.values(value).flatMap(stringsIn);
  return [];
}
