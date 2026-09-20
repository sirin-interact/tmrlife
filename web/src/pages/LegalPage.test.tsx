import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { MEDICAL_NOTICE_TEXT } from '@/components/MedicalNotice';
import { LEGAL_DOCUMENTS, LEGAL_TEXT } from '@/content/legal/documents';
import { LEGAL_DOCUMENTS_ARE_DRAFT } from '@/content/legal/status';
import { mockApi, signedIn, signedOut } from '@/test/mockApi';
import { renderRoute } from '@/test/renderRoute';

describe('약관과 개인정보 처리방침 화면', () => {
  it.each(Object.values(LEGAL_DOCUMENTS))(
    '/legal/$slug: 로그인하지 않아도 열리고, 제목과 판과 모든 항목을 보여 준다',
    async (legal) => {
      mockApi({ 'GET /api/v1/me': signedOut });

      const { router } = renderRoute(`/legal/${legal.slug}`);

      expect(await screen.findByRole('heading', { level: 1, name: legal.title })).toBeVisible();
      expect(screen.getByText(legal.version)).toHaveAttribute('datetime', legal.version);
      for (const section of legal.sections) {
        expect(screen.getByRole('heading', { level: 2, name: section.heading })).toBeVisible();
      }
      expect(router.state.location.pathname).toBe(`/legal/${legal.slug}`);
    },
  );

  it('초안인 동안에는 초안이라고 밝힌다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut });

    renderRoute('/legal/privacy');

    await screen.findByRole('heading', { level: 1 });
    // 검토를 마치고 표시를 내리면 안내도 함께 사라져야 한다.
    expect(screen.queryByText(LEGAL_TEXT.draftNotice) !== null).toBe(LEGAL_DOCUMENTS_ARE_DRAFT);
  });

  it('약관에는 의료 서비스가 아니라는 고정 고지가 글자 그대로 들어 있다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut });

    renderRoute('/legal/terms');

    expect(await screen.findByRole('note')).toHaveTextContent(MEDICAL_NOTICE_TEXT);
  });

  it('주소가 문서의 한 자리를 가리키면 그 제목으로 초점을 옮긴다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut });

    renderRoute('/legal/privacy#overseas-transfer');

    expect(
      await screen.findByRole('heading', { level: 2, name: '외부 AI 서비스 이용과 국외 이전' }),
    ).toHaveFocus();
  });

  it('국외 이전 항목은 받는 곳과 나라, 보내는 것, 목적, 보관을 표로 보여 준다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut });

    renderRoute('/legal/privacy');

    const section = await screen.findByRole('region', {
      name: '외부 AI 서비스 이용과 국외 이전',
    });
    const terms = within(section)
      .getAllByRole('term')
      .map((term) => term.textContent);
    expect(terms).toEqual([
      '받는 곳',
      '처리되는 나라',
      '보내는 것',
      '보내는 때와 방법',
      '목적',
      '받는 곳의 보관',
    ]);
  });

  it('로그인한 사람에게도 열린다', async () => {
    mockApi({ 'GET /api/v1/me': signedIn });

    renderRoute('/legal/terms');

    expect(await screen.findByRole('heading', { level: 1, name: '서비스 이용약관' })).toBeVisible();
  });

  it('없는 문서의 주소에서는 없는 화면을 보여 준다', async () => {
    mockApi({ 'GET /api/v1/me': signedOut });

    renderRoute('/legal/cookies');

    expect(
      await screen.findByRole('heading', { level: 1, name: '페이지를 찾지 못했어요' }),
    ).toBeInTheDocument();
  });
});
