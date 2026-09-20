import { describe, expect, it } from 'vitest';

import { LEGAL_DOCUMENTS, type LegalBlock, type LegalDocument } from '@/content/legal/documents';

// 공개 문서에 쓰지 않기로 한 말들. 의료 서비스가 아니라는 고정 고지는 따로 있는 컴포넌트가 맡는다.
// '상담'은 기관의 정식 이름에만 나오므로 문서 본문에는 쓰지 않는다.
const AVOIDED_WORDS = ['우울', '진단', '판정', '검사', '위험도', '환자', '증상', '치료', '상담'];

function blockTexts(block: LegalBlock): string[] {
  switch (block.kind) {
    case 'paragraph':
      return [block.text];
    case 'list':
      return [...block.items];
    case 'facts':
      return block.rows.flat();
    case 'medical-notice':
      return [];
  }
}

function allText(document: LegalDocument): string {
  return [
    document.title,
    document.summary,
    ...document.sections.flatMap((section) => [
      section.heading,
      ...section.blocks.flatMap(blockTexts),
    ]),
  ].join('\n');
}

function sectionText(document: LegalDocument, id: string): string {
  const section = document.sections.find((item) => item.id === id);
  expect(section, `${document.slug}#${id}`).toBeDefined();
  return section!.blocks.flatMap(blockTexts).join('\n');
}

describe('약관과 개인정보 처리방침', () => {
  it.each(Object.values(LEGAL_DOCUMENTS))(
    '$slug: 판은 날짜 꼴이고 자리의 이름은 겹치지 않는다',
    (document) => {
      expect(document.version).toMatch(/^\d{4}-\d{2}-\d{2}$/);
      const ids = document.sections.map((section) => section.id);
      expect(new Set(ids).size).toBe(ids.length);
    },
  );

  it.each(AVOIDED_WORDS)('"%s"이라는 말을 쓰지 않는다', (word) => {
    for (const document of Object.values(LEGAL_DOCUMENTS)) {
      expect(allText(document)).not.toContain(word);
    }
  });

  it('약관은 의료 서비스가 아니라는 고지와 위급할 때의 안내를 담는다', () => {
    const { terms } = LEGAL_DOCUMENTS;

    expect(terms.sections.flatMap((section) => section.blocks)).toContainEqual({
      kind: 'medical-notice',
    });
    expect(sectionText(terms, 'urgent')).toMatch(/119.*109/);
  });

  describe('개인정보 처리방침이 밝히는 것', () => {
    const { privacy } = LEGAL_DOCUMENTS;

    it('무엇을 받는지: 이메일, 접속 기록, 대화와 일기, 마음 신호', () => {
      const text = sectionText(privacy, 'collected');

      for (const item of [
        '이메일 주소',
        'IP 주소',
        '브라우저 정보',
        '대화에 쓴 글',
        '일기',
        '마음 신호',
      ]) {
        expect(text).toContain(item);
      }
    });

    it('음성은 저장하지 않는다', () => {
      expect(sectionText(privacy, 'voice')).toContain('음성은 저장하지 않아요');
    });

    it('글이 외부 AI 서비스로 가고 해외에서 처리될 수 있다: 받는 곳, 나라, 항목, 목적, 보관, 거부', () => {
      const text = sectionText(privacy, 'overseas-transfer');

      for (const item of ['받는 곳', '처리되는 나라', '보내는 것', '목적', '받는 곳의 보관']) {
        expect(text).toContain(item);
      }
      expect(text).toContain('Google');
      expect(text).toContain('해외');
      expect(text).toContain('동의하지 않으면');
    });

    it('사용자마다 다른 키로 암호화한다는 것과, 암호화하지 않는 값이 있다는 것을 함께 말한다', () => {
      const text = sectionText(privacy, 'protection');

      expect(text).toContain('사용자마다 다른 키로 암호화');
      expect(text).toContain('암호화하지 않아요');
    });

    it('지우기와 동의 철회, 그리고 신호를 취소한 기록이 점검을 위해 남는다는 것', () => {
      const deletion = sectionText(privacy, 'deletion');
      expect(deletion).toContain('하루 지우기');
      expect(deletion).toContain('탈퇴');
      expect(deletion).toContain('동의 철회');

      const sensitive = sectionText(privacy, 'sensitive-data');
      expect(sensitive).toContain('취소했다는 기록은 남겨요');
      expect(sensitive).toContain('점검');
    });

    it('문의처의 자리가 있다', () => {
      expect(sectionText(privacy, 'contact')).toContain('문의처');
    });
  });
});
