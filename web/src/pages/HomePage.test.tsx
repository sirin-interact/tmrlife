import { screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { json, mockApi, testMe } from '@/test/mockApi';
import { renderRoute } from '@/test/renderRoute';

const meWith = (user: Partial<typeof testMe.user>) => () =>
  json(200, { ...testMe, user: { ...testMe.user, ...user } });

afterEach(() => {
  vi.useRealTimers();
});

describe('HomePage', () => {
  it('부를 이름이 있으면 이름을 불러 인사한다', async () => {
    mockApi({ 'GET /api/v1/me': meWith({ display_name: '새벽' }) });

    renderRoute('/');

    expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent(
      /^새벽님, 안녕하세요$/,
    );
  });

  it('부를 이름을 정하지 않았으면 이름 없이 인사한다', async () => {
    mockApi({ 'GET /api/v1/me': meWith({ display_name: null }) });

    renderRoute('/');

    expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent(/^안녕하세요$/);
  });

  it('자정을 넘긴 새벽에는 달력 날짜가 아니라 기록 날짜(전날)를 보여 준다', async () => {
    // 서울은 9월 21일 새벽 1시 30분이다. 지금 나누는 이야기는 서버가 20일의 기록으로 남긴다.
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-09-20T16:30:00.000Z'));
    mockApi({ 'GET /api/v1/me': meWith({ timezone: 'Asia/Seoul' }) });

    renderRoute('/');

    const date = await screen.findByText('2026년 9월 20일 일요일');
    expect(date).toHaveAttribute('datetime', '2026-09-20');
  });

  it('새벽 4시부터는 새 날짜를 보여 준다', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-09-20T19:00:00.000Z'));
    mockApi({ 'GET /api/v1/me': meWith({ timezone: 'Asia/Seoul' }) });

    renderRoute('/');

    const date = await screen.findByText('2026년 9월 21일 월요일');
    expect(date).toHaveAttribute('datetime', '2026-09-21');
  });

  it('브라우저가 모르는 시간대여도 화면이 깨지지 않는다', async () => {
    mockApi({ 'GET /api/v1/me': meWith({ timezone: 'Mars/Olympus_Mons' }) });

    renderRoute('/');

    expect(await screen.findByText(/^\d{4}년 \d{1,2}월 \d{1,2}일 .요일$/)).toBeInTheDocument();
  });

  it('"오늘 이야기하기"는 대화 화면으로, "일기장 보기"는 일기장으로 간다', async () => {
    mockApi({ 'GET /api/v1/me': meWith({}) });

    renderRoute('/');

    expect(await screen.findByRole('link', { name: '오늘 이야기하기' })).toHaveAttribute(
      'href',
      '/talk',
    );
    expect(screen.getByRole('link', { name: '일기장 보기' })).toHaveAttribute('href', '/diary');
  });

  it('화면의 제목은 서비스 이름이다', async () => {
    mockApi({ 'GET /api/v1/me': meWith({}) });

    renderRoute('/');

    await screen.findByRole('link', { name: '오늘 이야기하기' });
    expect(document.title).toBe('내일');
  });

  it('로그아웃 버튼이 있다', async () => {
    mockApi({ 'GET /api/v1/me': meWith({}) });

    renderRoute('/');

    expect(await screen.findByRole('button', { name: '로그아웃' })).toBeEnabled();
  });
});
