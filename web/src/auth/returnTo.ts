import type { Location } from 'react-router';

/** 로그인 화면으로 보낼 때 함께 넘기는 값. 로그인 뒤에 돌아갈 앱 안의 주소다. */
export interface ReturnToState {
  from: string;
}

const HOME = '/';
// 로그인한 사람이 돌아갈 곳이 로그인 화면이면 제자리를 맴돈다.
const GUEST_ONLY_PATHS = ['/login', '/signup'];

export function returnToState(location: Location): ReturnToState {
  return { from: `${location.pathname}${location.search}${location.hash}` };
}

/**
 * 로그인 뒤에 돌아갈 주소를 고른다.
 * state는 브라우저 기록에 남아 있던 값이라 꼴을 믿지 않는다. 앱 안의 경로가 아니면 처음 화면으로 보낸다.
 */
export function returnPathFrom(state: unknown): string {
  if (typeof state !== 'object' || state === null || !('from' in state)) return HOME;

  const { from } = state;
  if (typeof from !== 'string') return HOME;
  // '//host'와 '/\host'는 브라우저가 다른 사이트 주소로 읽는다.
  if (!from.startsWith('/') || from.startsWith('//') || from.startsWith('/\\')) return HOME;

  const path = from.split(/[?#]/, 1)[0];
  if (path !== undefined && GUEST_ONLY_PATHS.includes(path)) return HOME;
  return from;
}
