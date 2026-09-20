import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router';

import { fetchResources } from '@/api/resources';
import { MedicalNotice } from '@/components/MedicalNotice';
import { ResourceList } from '@/components/ResourceList';
import { FALLBACK_RESOURCES, HELP_TEXT } from '@/content/helpText';
import { LEGAL_DOCUMENTS, legalPath } from '@/content/legal/documents';

const resourcesQueryKey = ['resources'] as const;

/**
 * 언제든, 로그인하지 않아도 열리는 화면. 연락할 수 있는 곳을 보여 주고 누르면 바로 전화가 걸린다.
 * 목록을 기다리거나 받지 못한 동안에도 번호는 보여야 한다. 그래서 기본 번호를 먼저 그리고, 받으면 서버의 목록으로 바꾼다.
 */
export function HelpPage() {
  const resources = useQuery({
    queryKey: resourcesQueryKey,
    queryFn: ({ signal }) => fetchResources(signal),
    // 자주 바뀌지 않는 목록이다. 화면을 오갈 때마다 다시 받지 않는다.
    staleTime: 60 * 60_000,
  });

  const fromServer = resources.data !== undefined && resources.data.length > 0;
  const items = fromServer ? resources.data : FALLBACK_RESOURCES;

  return (
    <div className="flex flex-col gap-6">
      <title>도움이 필요할 때 · 내일</title>
      <div className="flex flex-col gap-3">
        <h1 className="text-2xl leading-snug font-semibold">{HELP_TEXT.title}</h1>
        <p className="leading-relaxed text-muted-foreground">{HELP_TEXT.lead}</p>
      </div>

      <ResourceList resources={items} />

      {resources.isError && !fromServer && (
        <p role="status" className="text-sm text-muted-foreground">
          {HELP_TEXT.fallbackNote}
        </p>
      )}

      <div className="flex flex-col gap-2 leading-relaxed text-muted-foreground">
        <p>{HELP_TEXT.urgent}</p>
        <p>{HELP_TEXT.someone}</p>
      </div>

      <MedicalNotice />

      <nav
        aria-label={HELP_TEXT.documents}
        className="flex flex-wrap gap-x-4 border-t pt-4 text-sm"
      >
        {Object.values(LEGAL_DOCUMENTS).map((legal) => (
          <Link
            key={legal.slug}
            to={legalPath(legal.slug)}
            className="inline-flex min-h-touch items-center text-link underline underline-offset-4"
          >
            {legal.title}
          </Link>
        ))}
      </nav>
    </div>
  );
}
