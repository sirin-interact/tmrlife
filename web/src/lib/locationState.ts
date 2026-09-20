/**
 * 화면을 옮기면서 함께 넘긴 값(location.state)에서 하나를 꺼낸다.
 * 브라우저 기록에 남아 있던 값이라 꼴을 믿지 않는다. 받은 쪽이 타입을 확인하고 쓴다.
 */
export function stateValue(state: unknown, key: string): unknown {
  if (typeof state !== 'object' || state === null || !(key in state)) return undefined;
  return (state as Record<string, unknown>)[key];
}
