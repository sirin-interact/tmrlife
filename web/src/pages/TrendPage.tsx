import { useQuery } from '@tanstack/react-query';
import { ArrowUpRightIcon, SproutIcon } from 'lucide-react';
import { Link } from 'react-router';

import type { Trend } from '@/api/types';
import { useMe } from '@/auth/session';
import { MedicalNotice } from '@/components/MedicalNotice';
import { DotCalendar, DotLegend } from '@/components/trend/DotCalendar';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardFooter, CardHeader } from '@/components/ui/card';
import { describeError } from '@/content/errorMessages';
import { TREND_TEXT } from '@/content/trendText';
import { formatRecordDate, recordDateOf } from '@/lib/recordDate';
import { useNow } from '@/lib/useNow';
import { windowLength } from '@/trend/days';
import { emptyKindOf, type EmptyKind } from '@/trend/emptyState';
import { trendQueryOptions } from '@/trend/queries';
import '@/styles/journal.css';

/** 하루의 근거 화면으로 가는 주소. 날짜를 누르면 그날 무슨 말에서 그 점이 나왔는지 볼 수 있다. */
function dayHref(date: string): string {
  return `/signals/${date}`;
}

function EmptyNotice({ kind, trend }: { kind: Exclude<EmptyKind, null>; trend: Trend }) {
  // 이야기한 날이 하나도 없는 두 경우. 처음 온 사람에게는 첫 대화로 이끌고,
  // 앞선 기록이 있는 사람에게는 그 기록이 그대로 있다는 것을 먼저 말한다. 기록이 없다고 말하지 않는다.
  if (kind === 'no_records' || kind === 'window_empty') {
    const fresh = kind === 'no_records';
    return (
      <Card className="journal-trend-empty">
        <CardHeader>
          <h2 className="text-xl leading-snug font-semibold">
            {fresh ? TREND_TEXT.noRecordsTitle : TREND_TEXT.windowEmptyTitle}
          </h2>
        </CardHeader>
        <CardContent>
          <p className="text-muted-foreground">
            {fresh ? TREND_TEXT.noRecordsBody : TREND_TEXT.windowEmptyBody}
          </p>
        </CardContent>
        <CardFooter>
          <Button asChild size="lg" className="w-full">
            <Link to="/talk">
              {TREND_TEXT.startTalking}
              <ArrowUpRightIcon aria-hidden="true" />
            </Link>
          </Button>
        </CardFooter>
      </Card>
    );
  }

  const insufficient = kind === 'insufficient';
  return (
    <Card className="journal-trend-empty">
      <CardHeader>
        <h2 className="text-xl leading-snug font-semibold">
          {insufficient ? TREND_TEXT.insufficientTitle : TREND_TEXT.baselinePendingTitle}
        </h2>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        <p className="text-muted-foreground">
          {insufficient ? TREND_TEXT.insufficientBody : TREND_TEXT.baselinePendingBody}
        </p>
        <p className="text-sm text-muted-foreground">
          {TREND_TEXT.insufficientCount(
            windowLength(trend.from, trend.to),
            trend.conversation_days,
          )}
        </p>
      </CardContent>
    </Card>
  );
}

/**
 * 변화 추세 화면.
 *
 * 추정 점수, 구간, 신뢰도, 개입 단계는 이 화면에 오지 않는다. 서버가 그 값을 아예 보내지 않고,
 * 이 화면도 계산하지 않는다. 힘든 사람에게 점수와 경고는 도움보다 상처가 되기 쉽고, 이 값은 검사 결과가 아니다.
 * 보여 주는 것은 "며칠 가운데 며칠이었는지"와 "평소와 견주어 어떤지"뿐이다.
 */
export function TrendPage() {
  const trend = useQuery(trendQueryOptions);
  const me = useMe();
  const now = useNow();
  const data = trend.data;
  const empty = data === undefined ? null : emptyKindOf(data);
  // 오늘의 분석이 아직 없을 때만 "정리가 끝나면 나타나요"라고 말한다.
  // 오늘 칸에 점이 이미 찍혀 있는데 곧 나타난다고 적으면 그 칸을 믿을 수 없게 된다.
  const todayPending =
    data !== undefined && data.as_of !== recordDateOf(now, me.data?.user.timezone);

  return (
    <div className="journal-page journal-trend-page">
      <title>{TREND_TEXT.pageTitle}</title>

      <header className="journal-page-heading">
        <div>
          <p className="journal-eyebrow">{TREND_TEXT.eyebrow}</p>
          <h1>{TREND_TEXT.title}</h1>
          <p className="journal-page-lead">{TREND_TEXT.lead}</p>
          {data !== undefined && (
            <p className="journal-as-of">
              <time dateTime={data.as_of}>{TREND_TEXT.asOf(formatRecordDate(data.as_of))}</time>
              {todayPending && ` ${TREND_TEXT.asOfNote}`}
            </p>
          )}
        </div>
        <span className="journal-heading-icon" aria-hidden="true">
          <SproutIcon />
        </span>
      </header>

      {trend.isPending && (
        <p role="status" className="animate-appear-late text-muted-foreground">
          {TREND_TEXT.loading}
        </p>
      )}

      {trend.isError && data === undefined && (
        <Alert variant="destructive">
          <AlertDescription>
            <p>{describeError(trend.error).message}</p>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={trend.isFetching}
              onClick={() => void trend.refetch()}
            >
              {TREND_TEXT.retry}
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {data !== undefined && (
        <>
          {empty !== null && <EmptyNotice kind={empty} trend={data} />}

          {/* 기록이 모자라도 달력은 그린다. 빈 달력과 점 읽는 법이 함께 있으면 "아직 없다"가 고장처럼 보이지 않는다. */}
          <section className="journal-trend-card">
            <div className="journal-section-heading">
              <h2>{TREND_TEXT.calendarTitle}</h2>
              <span>
                {data.from.slice(5).replace('-', '.')} — {data.to.slice(5).replace('-', '.')}
              </span>
            </div>
            <DotCalendar
              rows={data.rows}
              // 평소가 없거나 기록이 모자란 동안에는 견주는 말을 붙이지 않는다. 일수는 그대로 보여 준다.
              withComparison={empty === null}
              dayHref={dayHref}
            />
            <DotLegend />
          </section>

          <p className="journal-trend-note">{TREND_TEXT.notATest}</p>
          <MedicalNotice />
        </>
      )}
    </div>
  );
}
