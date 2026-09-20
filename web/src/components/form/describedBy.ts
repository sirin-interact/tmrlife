/** aria-describedby에 넣을 값을 만든다. 가리킬 것이 없으면 속성 자체를 빼야 하므로 undefined를 돌려준다. */
export function describedBy(...ids: Array<string | false | undefined>): string | undefined {
  const present = ids.filter((id): id is string => typeof id === 'string' && id !== '');
  return present.length > 0 ? present.join(' ') : undefined;
}
