import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { MEDICAL_NOTICE_TEXT } from '@/components/MedicalNotice';
import { FALLBACK_RESOURCES, HELP_TEXT } from '@/content/helpText';
import { testResources } from '@/test/fakeSocket';
import { json, mockApi, networkFailure, signedIn, signedOut } from '@/test/mockApi';
import { renderRoute } from '@/test/renderRoute';

// 이 화면이 직접 하는 말에 쓰지 않기로 한 말들. 기관의 정식 이름(서버가 주는 값, 기본 번호의 이름)은 여기에 들지 않는다.
const AVOIDED_WORDS = ['우울', '진단', '판정', '검사', '위험도', '환자', '증상', '치료', '상담'];

const callLinks = () =>
  within(screen.getByRole('main'))
    .getAllByRole('link')
    .filter((link) => link.getAttribute('href')?.startsWith('tel:'));

describe('도움이 필요할 때', () => {
  it('로그인하지 않아도 열리고, 서버가 준 순서대로 번호를 보여 주며 누르면 바로 전화가 걸린다', async () => {
    const api = mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/resources': () => json(200, { items: testResources }),
    });

    const { router } = renderRoute('/help');

    expect(
      await screen.findByRole('heading', { level: 1, name: '도움이 필요할 때' }),
    ).toBeVisible();
    await waitFor(() => expect(api.callsTo('GET /api/v1/resources')).toHaveLength(1));
    await waitFor(() =>
      expect(callLinks().map((link) => link.getAttribute('href'))).toEqual([
        'tel:109',
        'tel:15770199',
        'tel:119',
      ]),
    );
    expect(callLinks()[0]).toHaveAccessibleName('자살예방상담전화 109 전화하기');
    expect(screen.getByText('24시간, 지금 바로 이야기를 들어줄 사람과 연결돼요.')).toBeVisible();
    expect(router.state.location.pathname).toBe('/help');
  });

  it('의료 서비스가 아니라는 고정 고지를 글자 그대로 보여 준다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/resources': () => json(200, { items: testResources }),
    });

    renderRoute('/help');

    expect(await screen.findByRole('note')).toHaveTextContent(MEDICAL_NOTICE_TEXT);
  });

  it('목록을 받지 못해도 빈 화면이 아니라 기본 번호를 보여 준다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut, 'GET /api/v1/resources': networkFailure });

    renderRoute('/help');

    // 받아 오는 동안에도, 받지 못한 뒤에도 번호는 보인다.
    expect(callLinks().map((link) => link.getAttribute('href'))).toEqual([
      'tel:109',
      'tel:119',
      'tel:15770199',
    ]);
    expect(await screen.findByText(HELP_TEXT.fallbackNote)).toBeInTheDocument();
    expect(callLinks()).toHaveLength(FALLBACK_RESOURCES.length);
  });

  it('로그인한 사람에게도 같은 화면이 열린다', async () => {
    mockApi({
      'GET /api/v1/me': signedIn,
      'GET /api/v1/resources': () => json(200, { items: testResources }),
    });

    renderRoute('/help');

    expect(
      await screen.findByRole('heading', { level: 1, name: '도움이 필요할 때' }),
    ).toBeVisible();
    expect(await screen.findByRole('button', { name: '로그아웃' })).toBeInTheDocument();
  });

  it('약관과 개인정보 처리방침으로 가는 링크가 있다', async () => {
    mockApi({
      'GET /api/v1/me': signedOut,
      'GET /api/v1/resources': () => json(200, { items: testResources }),
    });

    renderRoute('/help');

    expect(await screen.findByRole('link', { name: '서비스 이용약관' })).toHaveAttribute(
      'href',
      '/legal/terms',
    );
    expect(screen.getByRole('link', { name: '개인정보 처리방침' })).toHaveAttribute(
      'href',
      '/legal/privacy',
    );
  });

  it.each(AVOIDED_WORDS)('이 화면이 직접 하는 말에는 "%s"이라는 말을 쓰지 않는다', (word) => {
    for (const text of Object.values(HELP_TEXT)) {
      expect(text).not.toContain(word);
    }
  });
});
