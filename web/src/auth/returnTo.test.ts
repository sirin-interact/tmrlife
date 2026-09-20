import { describe, expect, it } from 'vitest';

import { returnPathFrom } from '@/auth/returnTo';

describe('로그인 뒤에 돌아갈 주소', () => {
  it.each([
    ['앱 안의 경로는 그대로 돌아간다', { from: '/diary/2026-09-20' }, '/diary/2026-09-20'],
    ['쿼리와 해시도 지킨다', { from: '/insights?range=month#top' }, '/insights?range=month#top'],
    ['값이 없으면 처음 화면이다', null, '/'],
    ['꼴이 다르면 처음 화면이다', { from: 42 }, '/'],
    ['다른 사이트 주소는 받지 않는다', { from: 'https://evil.example/login' }, '/'],
    ['//로 시작하는 주소는 다른 사이트로 읽히므로 받지 않는다', { from: '//evil.example' }, '/'],
    ['/\\로 시작하는 주소도 받지 않는다', { from: '/\\evil.example' }, '/'],
    ['로그인 화면으로 되돌아가지 않는다', { from: '/login' }, '/'],
    ['가입 화면으로 되돌아가지 않는다', { from: '/signup?ref=mail' }, '/'],
  ])('%s', (_name, state, expected) => {
    expect(returnPathFrom(state)).toBe(expected);
  });
});
