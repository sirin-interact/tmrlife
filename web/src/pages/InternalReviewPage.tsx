import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router';

import { isApiError } from '@/api/errors';
import { BaselineSection } from '@/components/review/BaselineSection';
import { ChangeSection } from '@/components/review/ChangeSection';
import { ConfidenceSection } from '@/components/review/ConfidenceSection';
import { ExtractorsSection } from '@/components/review/ExtractorsSection';
import { ParamsSection } from '@/components/review/ParamsSection';
import { ScoreSection } from '@/components/review/ScoreSection';
import { StageSection } from '@/components/review/StageSection';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardFooter, CardHeader } from '@/components/ui/card';
import { describeError } from '@/content/errorMessages';
import { REVIEW_TEXT } from '@/content/reviewText';
import { formatRecordDate } from '@/lib/recordDate';
import { internalReviewQueryOptions } from '@/review/queries';

/** 시연 계정도 관리자도 아닌 계정이 열었을 때. 실패가 아니라 답이므로 오류처럼 보이지 않게 둔다. */
function Refused() {
  return (
    <Card>
      <CardHeader>
        <h2 className="text-xl leading-snug font-semibold">{REVIEW_TEXT.forbiddenTitle}</h2>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <p className="text-muted-foreground">{REVIEW_TEXT.forbiddenBody}</p>
        <p className="text-sm text-muted-foreground">{REVIEW_TEXT.forbiddenWhy}</p>
      </CardContent>
      <CardFooter>
        <Button asChild variant="outline" className="w-full">
          <Link to="/">{REVIEW_TEXT.backHome}</Link>
        </Button>
      </CardFooter>
    </Card>
  );
}

/**
 * 계산이 그 값에 이른 과정을 중간값까지 그대로 펼치는 화면.
 *
 * 사용자 화면에서는 어디로도 이어지지 않는다. 만드는 사람과 살펴보는 사람이 주소를 직접 열어 본다.
 * 볼 수 있는 계정인지는 서버가 판단하고, 그렇지 않으면 이 화면이 조용히 그 사실만 말한다.
 *
 * 여기 있는 값은 모두 서버가 한 계산의 결과다. 화면은 어떤 값도 다시 계산하지 않는다.
 * 같은 식을 두 곳에 두면 화면에 적힌 과정과 실제로 쓰인 값이 조용히 어긋난다.
 */
export function InternalReviewPage() {
  const review = useQuery(internalReviewQueryOptions);
  const data = review.data;
  const refused = isApiError(review.error) && review.error.code === 'forbidden';

  return (
    <div className="flex flex-col gap-6">
      <title>{REVIEW_TEXT.pageTitle}</title>

      <header className="flex flex-col gap-2">
        <h1 className="text-2xl leading-snug font-semibold">{REVIEW_TEXT.title}</h1>
        {/* 화면에서 가장 먼저 읽혀야 하는 말이다. 숫자를 보기 전에 이 숫자가 무엇이 아닌지 먼저 읽어야 한다. */}
        <p role="note" className="rounded-xl border bg-card px-5 py-4 leading-relaxed">
          {REVIEW_TEXT.estimateNotice}
        </p>
        <p className="text-sm text-muted-foreground">{REVIEW_TEXT.audienceNotice}</p>
        {data !== undefined && (
          <p className="text-sm text-muted-foreground">
            <time dateTime={data.as_of}>{REVIEW_TEXT.asOf(formatRecordDate(data.as_of))}</time>{' '}
            {REVIEW_TEXT.asOfNote}
          </p>
        )}
      </header>

      {review.isPending && (
        <p role="status" className="animate-appear-late text-muted-foreground">
          {REVIEW_TEXT.loading}
        </p>
      )}

      {refused && <Refused />}

      {review.isError && !refused && data === undefined && (
        <Alert variant="destructive">
          <AlertDescription>
            <p>{describeError(review.error).message}</p>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={review.isFetching}
              onClick={() => void review.refetch()}
            >
              {REVIEW_TEXT.retry}
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {data !== undefined && (
        <>
          {/* 아래 숫자들보다 먼저 읽혀야 한다. 낱말 표로 채운 행이 섞여 있으면 그 아래의 모든 값이 그만큼 못 미더워진다. */}
          <ExtractorsSection extractors={data.extractors} />
          <ScoreSection score={data.score} params={data.params} />
          <ConfidenceSection confidence={data.confidence} params={data.params} />
          <BaselineSection baseline={data.baseline} params={data.params} />
          <ChangeSection change={data.change} params={data.params} />
          <StageSection stage={data.stage} params={data.params} />
          <ParamsSection params={data.params} />
        </>
      )}
    </div>
  );
}
