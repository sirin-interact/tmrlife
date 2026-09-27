import { describe, expect, it } from 'vitest';

import { CONSENT_COPY, CONSENT_TEXT, consentCopyFor } from '@/content/consentCopy';
import { LEGAL_DOCUMENTS } from '@/content/legal/documents';
import { AVOIDED_WORDS } from '@/test/avoidedWords';

describe('동의 문구', () => {
  const texts = [
    ...Object.values(CONSENT_COPY).flatMap(({ title, description, linkLabel }) => [
      title,
      description,
      linkLabel,
    ]),
    ...Object.values(CONSENT_TEXT),
  ];

  it('동의마다 이름과 설명이 있다', () => {
    for (const [kind, copy] of Object.entries(CONSENT_COPY)) {
      expect(copy.title.trim(), kind).not.toBe('');
      expect(copy.description.trim(), kind).not.toBe('');
    }
  });

  it('마음과 건강에 관한 민감정보, 외부 AI 서비스와 해외 처리를 분명히 말한다', () => {
    expect(CONSENT_COPY.sensitive_data.title).toContain('민감정보');
    expect(CONSENT_COPY.sensitive_data.description).toMatch(/마음.*건강/);
    expect(CONSENT_COPY.overseas_transfer.description).toContain('외부 AI 서비스');
    expect(CONSENT_COPY.overseas_transfer.description).toContain('해외');
  });

  it('국외 이전 동의는 글과 음성이 모두 나간다는 것, 쓰는 목적, 받는 곳, 거부하면 어떻게 되는지를 말한다', () => {
    const { description } = CONSENT_COPY.overseas_transfer;

    expect(description).toContain('대화 내용');
    expect(description).toContain('음성');
    expect(description).toMatch(/답을 만들고/);
    expect(description).toMatch(/글로 옮기고/);
    expect(description).toMatch(/소리로 바꾸/);
    expect(description).toContain('Google');
    expect(description).toContain('동의하지 않으면');
  });

  it('개인정보 동의는 이메일 말고도 접속 기록(IP 주소, 브라우저 정보)을 받는다고 말한다', () => {
    expect(CONSENT_COPY.privacy.description).toContain('IP 주소');
    expect(CONSENT_COPY.privacy.description).toContain('브라우저 정보');
  });

  it('민감정보 동의는 기록을 "돌아보는 데에만" 쓴다고 약속하지 않는다', () => {
    const { description } = CONSENT_COPY.sensitive_data;

    // 취소한 신호의 기록처럼, 내일이 말을 잘못 읽고 있지 않은지 점검하는 데에도 쓴다. 그 쓰임까지 적어야 한다.
    expect(description).not.toContain('돌아보는 데에만');
    expect(description).toContain('점검');
    expect(description).toContain('다른 사람에게 전달하지 않아요');
  });

  it.each(AVOIDED_WORDS)('"%s"이라는 말을 쓰지 않는다', (word) => {
    for (const text of texts) {
      expect(text).not.toContain(word);
    }
  });
});

describe('동의 문구와 판', () => {
  it('서버가 알려준 판이 글의 판과 같을 때만 글을 내준다', () => {
    const { version } = CONSENT_COPY.terms;

    expect(consentCopyFor('terms', version)).toBe(CONSENT_COPY.terms);
    expect(consentCopyFor('terms', '1999-01-01')).toBeUndefined();
    expect(consentCopyFor('marketing', version)).toBeUndefined();
    // 객체에 원래 있는 이름을 동의의 종류로 읽지 않는다.
    expect(consentCopyFor('constructor', version)).toBeUndefined();
  });

  it('동의의 판은 그 동의가 가리키는 문서의 판과 같다', () => {
    // 문서를 고치고 판을 올렸다면 그 문서를 보고 받는 동의의 판도 함께 올라야 한다.
    for (const [kind, copy] of Object.entries(CONSENT_COPY)) {
      expect(copy.version, kind).toBe(LEGAL_DOCUMENTS[copy.document.slug].version);
    }
  });

  it('동의가 가리키는 문서의 자리는 실제로 있다', () => {
    for (const [kind, copy] of Object.entries(CONSENT_COPY)) {
      const { slug, sectionId } = copy.document;
      if (sectionId === undefined) continue;
      const ids = LEGAL_DOCUMENTS[slug].sections.map((section) => section.id);
      expect(ids, kind).toContain(sectionId);
    }
  });
});
