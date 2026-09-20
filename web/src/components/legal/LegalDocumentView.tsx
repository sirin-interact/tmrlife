import { useEffect } from 'react';

import { MedicalNotice } from '@/components/MedicalNotice';
import { LEGAL_TEXT, type LegalBlock, type LegalDocument } from '@/content/legal/documents';
import { LEGAL_DOCUMENTS_ARE_DRAFT } from '@/content/legal/status';

interface LegalDocumentViewProps {
  document: LegalDocument;
  /** 제목 요소의 id. 이 문서를 감싸는 영역이 aria-labelledby로 가리킨다. */
  titleId: string;
  /** 화면의 제목이면 h1, 다른 화면 위에 겹쳐 띄운 것이면 h2다. */
  titleAs: 'h1' | 'h2';
  /** 열자마자 보여 줄 자리. 동의 항목이 문서의 한 부분을 가리킬 때 쓴다. */
  focusSectionId?: string;
}

function sectionDomId(sectionId: string): string {
  // 가입 화면 위에 겹쳐 띄우기도 한다. 그 화면의 id와 부딪히지 않게 앞에 이름을 붙인다.
  return `legal-${sectionId}`;
}

function Block({ block }: { block: LegalBlock }) {
  switch (block.kind) {
    case 'paragraph':
      return <p>{block.text}</p>;
    case 'list':
      return (
        <ul className="flex list-disc flex-col gap-2 pl-5">
          {block.items.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ul>
      );
    case 'facts':
      return (
        <dl className="flex flex-col gap-3 rounded-xl border bg-card px-5 py-4">
          {block.rows.map(([label, value]) => (
            <div key={label} className="flex flex-col gap-0.5">
              <dt className="text-sm font-semibold">{label}</dt>
              <dd className="text-muted-foreground">{value}</dd>
            </div>
          ))}
        </dl>
      );
    case 'medical-notice':
      return <MedicalNotice className="rounded-xl border bg-card px-5 py-4 text-base" />;
  }
}

/** 약관이나 개인정보 처리방침 한 편을 그린다. 화면으로도, 가입 화면 위에 겹쳐 띄운 것으로도 쓴다. */
export function LegalDocumentView({
  document: legal,
  titleId,
  titleAs: Title,
  focusSectionId,
}: LegalDocumentViewProps) {
  const SectionHeading = Title === 'h1' ? 'h2' : 'h3';

  useEffect(() => {
    if (focusSectionId === undefined) return;
    const target = window.document.getElementById(sectionDomId(focusSectionId));
    // 가리키는 자리의 제목으로 초점까지 옮긴다. 화면 낭독기가 문서의 처음이 아니라 그 자리부터 읽는다.
    target?.scrollIntoView?.({ block: 'start' });
    target?.focus({ preventScroll: true });
  }, [focusSectionId]);

  return (
    <article className="flex flex-col gap-8 leading-relaxed">
      <header className="flex flex-col gap-3">
        <Title
          id={titleId}
          tabIndex={-1}
          className="text-2xl leading-snug font-semibold outline-none"
        >
          {legal.title}
        </Title>
        <p className="text-muted-foreground">{legal.summary}</p>
        <p className="text-sm text-muted-foreground">
          {LEGAL_TEXT.versionLabel} <time dateTime={legal.version}>{legal.version}</time>
        </p>
        {LEGAL_DOCUMENTS_ARE_DRAFT && (
          <p className="rounded-xl border border-input bg-muted px-5 py-4 text-sm">
            {LEGAL_TEXT.draftNotice}
          </p>
        )}
      </header>

      {legal.sections.map((section) => (
        <section
          key={section.id}
          aria-labelledby={sectionDomId(section.id)}
          className="flex flex-col gap-3"
        >
          <SectionHeading
            id={sectionDomId(section.id)}
            tabIndex={-1}
            className="scroll-mt-6 text-lg leading-snug font-semibold outline-none"
          >
            {section.heading}
          </SectionHeading>
          {section.blocks.map((block, index) => (
            // 문서의 조각은 순서가 바뀌지 않는 고정된 글이라 자리 번호를 키로 써도 된다.
            <Block key={index} block={block} />
          ))}
        </section>
      ))}
    </article>
  );
}
