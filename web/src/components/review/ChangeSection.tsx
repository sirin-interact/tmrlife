import type { ReviewChange, ReviewParams } from '@/api/types';
import { formatDecimal } from '@/components/chart/format';
import { SeriesChart, type SeriesPoint } from '@/components/chart/SeriesChart';
import {
  Cell,
  Fact,
  FactList,
  Flag,
  ReviewCard,
  ReviewTable,
  RowHead,
} from '@/components/review/ReviewCard';
import { REVIEW_TEXT } from '@/content/reviewText';
import { dayOfMonth } from '@/trend/days';
import { formatRecordDateShort } from '@/lib/recordDate';

/**
 * 평소에서 벗어난 정도를 날마다 쌓은 흐름.
 *
 * 감지 여부는 누적값을 한계값과 견주어 화면이 판단하지 않는다. 서버가 보낸 `detected`를 읽는다.
 * 한계값을 넘은 뒤에도 0으로 되돌리지 않기 때문에, 눈으로 견주면 이미 풀린 날도 감지로 보인다.
 */
export function ChangeSection({ change, params }: { change: ReviewChange; params: ReviewParams }) {
  const top = Math.max(params.cusum_h, ...change.series.map((point) => point.s));
  const max = Math.max(1, Math.ceil(top + 1));

  const points: SeriesPoint[] = change.series.map((point, index) => {
    const before = index === 0 ? false : change.series[index - 1]!.detected;
    const marker =
      point.detected && !before
        ? REVIEW_TEXT.changeDetectionOn
        : !point.detected && before
          ? REVIEW_TEXT.changeDetectionOff
          : undefined;
    return {
      label: String(dayOfMonth(point.date)),
      value: point.s,
      marker,
      // 풀린 날은 누적값이 한계값 바로 아래에 있다. 이름표를 위에 두면 한계값 선과 그 이름표에 겹친다.
      markerBelow: marker === REVIEW_TEXT.changeDetectionOff,
      hint: `${formatRecordDateShort(point.date)} · 관찰 ${point.observed} · 누적 ${formatDecimal(point.s)}`,
    };
  });

  return (
    <ReviewCard
      title={REVIEW_TEXT.changeTitle}
      headline={
        <p className="flex flex-wrap items-baseline gap-x-3">
          <span className="text-2xl font-semibold">
            {!change.running
              ? REVIEW_TEXT.changeNotRunning
              : change.detected
                ? REVIEW_TEXT.changeDetected
                : REVIEW_TEXT.changeNotDetected}
          </span>
          <span className="text-muted-foreground tabular-nums">
            {REVIEW_TEXT.changeS} {formatDecimal(change.s)}
          </span>
        </p>
      }
    >
      <FactList>
        <Fact label={REVIEW_TEXT.changeS}>{formatDecimal(change.s)}</Fact>
      </FactList>
      {change.from != null && (
        <p className="text-sm text-muted-foreground">
          {REVIEW_TEXT.changeFrom(formatRecordDateShort(change.from))}
        </p>
      )}
      <p className="text-sm text-muted-foreground">
        {REVIEW_TEXT.changeParams(params.cusum_k, params.cusum_h)}
      </p>
      <p className="text-sm text-muted-foreground">
        {REVIEW_TEXT.changeCeiling(params.cusum_max_step, params.cusum_max_s)}
      </p>

      {points.length === 0 ? (
        <p className="text-muted-foreground">{REVIEW_TEXT.changeEmpty}</p>
      ) : (
        <>
          <SeriesChart
            label={REVIEW_TEXT.changeChartLabel}
            points={points}
            max={max}
            yTicks={[0, max]}
            reference={{
              value: params.cusum_h,
              label: REVIEW_TEXT.changeThreshold(params.cusum_h),
            }}
          />
          <ReviewTable
            label={REVIEW_TEXT.changeChartLabel}
            columns={[
              REVIEW_TEXT.changeColumns.date,
              REVIEW_TEXT.changeColumns.observed,
              REVIEW_TEXT.changeColumns.step,
              REVIEW_TEXT.changeColumns.s,
              REVIEW_TEXT.changeColumns.detected,
            ]}
            scroll
          >
            {change.series.map((point) => (
              <tr key={point.date} className="border-b last:border-0">
                <RowHead>
                  <time dateTime={point.date}>{formatRecordDateShort(point.date)}</time>
                </RowHead>
                <Cell>{point.observed}</Cell>
                <Cell>
                  {formatDecimal(point.step)}
                  {point.capped && (
                    <span className="ml-1 text-xs text-muted-foreground">
                      {REVIEW_TEXT.changeCapped}
                    </span>
                  )}
                </Cell>
                <Cell>
                  {formatDecimal(point.s)}
                  {point.at_ceiling && (
                    <span className="ml-1 text-xs text-muted-foreground">
                      {REVIEW_TEXT.changeAtCeiling}
                    </span>
                  )}
                </Cell>
                <Cell>
                  <Flag on={point.detected} />
                </Cell>
              </tr>
            ))}
          </ReviewTable>
        </>
      )}
    </ReviewCard>
  );
}
