import type { ReviewExtractor } from '@/api/types';
import { Cell, Flag, ReviewCard, ReviewTable, RowHead } from '@/components/review/ReviewCard';
import { REVIEW_TEXT } from '@/content/reviewText';

/**
 * 키 없이 띄운 서버가 쓰는 모델의 이름. 표시의 뒷부분이 이 이름이면 낱말 표로 채운 행이다.
 *
 * 이 이름을 여기서 아는 까닭: 저장된 판단과 근거만 보고는 낱말 표가 채운 행을 가려낼 수 없다.
 * 화면에 아무 표시가 없으면 '피로 관찰됨' 옆에 '피곤하지도 않아'가 근거로 붙은 것도 모델이 읽은 판단으로 읽힌다.
 */
const SCRIPTED_MODEL = 'scripted';

const isScripted = (version: string) => version.endsWith(`/${SCRIPTED_MODEL}`);

/** 내부 확인 화면에서만 보는 값이라 기기의 시간대로 적는다. 견줄 대상은 옆 줄의 다른 추출기뿐이다. */
function formatMoment(value: string): string {
  const at = new Date(value);
  if (Number.isNaN(at.getTime())) return value;
  return at.toLocaleString('ko-KR', {
    year: 'numeric',
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

/**
 * 이 계정의 신호 행을 남긴 추출기들.
 *
 * 화면에 반드시 띄운다. 키 없이 띄운 서버도 낱말 표로 여덟 항목을 모두 채우기 때문에,
 * 이 표시가 없으면 다른 화면에 보이는 판단과 근거가 실제 모델이 읽은 것인지 가릴 수 없다.
 */
export function ExtractorsSection({ extractors }: { extractors: readonly ReviewExtractor[] }) {
  const scriptedRows = extractors
    .filter((entry) => isScripted(entry.version))
    .reduce((sum, entry) => sum + entry.rows, 0);

  return (
    <ReviewCard
      title={REVIEW_TEXT.extractorsTitle}
      headline={
        extractors.length === 0 ? undefined : (
          <p className={scriptedRows > 0 ? 'font-semibold' : 'text-muted-foreground'}>
            {scriptedRows > 0
              ? REVIEW_TEXT.extractorsScriptedWarning(scriptedRows)
              : REVIEW_TEXT.extractorsAllReal}
          </p>
        )
      }
    >
      <p className="text-sm text-muted-foreground">{REVIEW_TEXT.extractorsNote}</p>

      {extractors.length === 0 ? (
        <p className="text-muted-foreground">{REVIEW_TEXT.extractorsNone}</p>
      ) : (
        <ReviewTable
          label={REVIEW_TEXT.extractorsTitle}
          columns={[
            REVIEW_TEXT.extractorsColumns.version,
            REVIEW_TEXT.extractorsColumns.rows,
            REVIEW_TEXT.extractorsColumns.lastAt,
          ]}
        >
          {extractors.map((entry) => (
            <tr key={entry.version} className="border-b last:border-0">
              <RowHead>
                <span className="flex flex-wrap items-center gap-2">
                  <code className="font-mono text-xs">{entry.version}</code>
                  {isScripted(entry.version) && (
                    <Flag on label={REVIEW_TEXT.extractorsScriptedBadge} />
                  )}
                </span>
              </RowHead>
              <Cell>{entry.rows}</Cell>
              <Cell className="whitespace-nowrap">
                <time dateTime={entry.last_at}>{formatMoment(entry.last_at)}</time>
              </Cell>
            </tr>
          ))}
        </ReviewTable>
      )}
    </ReviewCard>
  );
}
