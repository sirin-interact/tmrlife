import { useId } from 'react';
import { Link, useLocation, useParams } from 'react-router';

import { LegalDocumentView } from '@/components/legal/LegalDocumentView';
import { isLegalSlug, LEGAL_DOCUMENTS, legalPath } from '@/content/legal/documents';
import { NotFoundPage } from '@/pages/NotFoundPage';

function sectionFromHash(hash: string): string | undefined {
  if (hash.length <= 1) return undefined;
  try {
    return decodeURIComponent(hash.slice(1));
  } catch {
    // 깨진 주소다. 문서의 처음부터 보여 준다.
    return undefined;
  }
}

/** 약관과 개인정보 처리방침을 읽는 화면. 가입하기 전에 읽는 글이라 로그인하지 않아도 열린다. */
export function LegalPage() {
  const { slug } = useParams();
  const { hash } = useLocation();
  const titleId = useId();

  if (!isLegalSlug(slug)) return <NotFoundPage />;
  const legal = LEGAL_DOCUMENTS[slug];
  const others = Object.values(LEGAL_DOCUMENTS).filter((other) => other.slug !== slug);

  return (
    <div className="flex flex-col gap-8">
      <title>{`${legal.title} · 내일`}</title>
      <LegalDocumentView
        document={legal}
        titleId={titleId}
        titleAs="h1"
        // 주소의 # 뒤가 문서의 한 자리를 가리키면 거기부터 보여 준다.
        focusSectionId={sectionFromHash(hash)}
      />
      <nav aria-label="다른 문서" className="flex flex-wrap gap-x-4 border-t pt-4 text-sm">
        {others.map((other) => (
          <Link
            key={other.slug}
            to={legalPath(other.slug)}
            className="inline-flex min-h-touch items-center text-link underline underline-offset-4"
          >
            {other.title}
          </Link>
        ))}
        <Link
          to="/"
          className="inline-flex min-h-touch items-center text-link underline underline-offset-4"
        >
          처음으로
        </Link>
      </nav>
    </div>
  );
}
