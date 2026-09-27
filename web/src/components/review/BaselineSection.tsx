import type { ReviewBaseline, ReviewParams } from '@/api/types';
import { formatDecimal } from '@/components/chart/format';
import {
  Cell,
  Fact,
  FactList,
  Flag,
  ReviewCard,
  ReviewTable,
  RowHead,
} from '@/components/review/ReviewCard';
import { ITEM_LABEL, REVIEW_TEXT, TREND_ROW_LABEL } from '@/content/reviewText';
import { formatRecordDateShort } from '@/lib/recordDate';

/**
 * 그 사람의 평소. 변화 탐지가 무엇에서 벗어난 정도를 쌓는지 여기서 읽는다.
 *
 * 아직 잡히지 않은 동안의 값도 그대로 보여 주되, 평소로 쓰면 안 되는 값이라고 적어 둔다.
 */
export function BaselineSection({
  baseline,
  params,
}: {
  baseline: ReviewBaseline;
  params: ReviewParams;
}) {
  const window =
    baseline.start == null
      ? REVIEW_TEXT.baselineNoStart
      : baseline.end == null
        ? REVIEW_TEXT.baselineWindowOpen(formatRecordDateShort(baseline.start))
        : `${formatRecordDateShort(baseline.start)} ~ ${formatRecordDateShort(baseline.end)}`;

  return (
    <ReviewCard
      title={REVIEW_TEXT.baselineTitle}
      headline={
        <p className="flex flex-wrap items-baseline gap-x-3">
          <span className="text-2xl font-semibold">
            {baseline.established ? REVIEW_TEXT.baselineEstablished : REVIEW_TEXT.baselinePending}
          </span>
          <span className="text-muted-foreground tabular-nums">
            {REVIEW_TEXT.baselineMu} {formatDecimal(baseline.mu)}
          </span>
        </p>
      }
    >
      {!baseline.established && (
        <p className="text-sm text-muted-foreground">{REVIEW_TEXT.baselinePendingNote}</p>
      )}

      <FactList>
        <Fact label={REVIEW_TEXT.baselineWindow}>{window}</Fact>
        <Fact label={REVIEW_TEXT.baselineDays}>{REVIEW_TEXT.days(baseline.days)}</Fact>
        <Fact label={REVIEW_TEXT.baselineObservedTotal}>{baseline.observed_total}</Fact>
        <Fact label={REVIEW_TEXT.baselineMu}>
          {formatDecimal(baseline.mu)}{' '}
          <span className="text-muted-foreground">
            ({REVIEW_TEXT.baselineMuFormula(baseline.observed_total, baseline.days)})
          </span>
        </Fact>
      </FactList>

      {baseline.extended && <Flag on label={REVIEW_TEXT.baselineExtended} />}

      <p className="text-sm text-muted-foreground">
        {REVIEW_TEXT.baselineParams(
          params.baseline_window_days,
          params.baseline_min_conversation_days,
        )}
      </p>

      <ReviewTable
        label={REVIEW_TEXT.baselineTrendRates}
        columns={[REVIEW_TEXT.baselineTrendRates, REVIEW_TEXT.scoreColumns.observedDays]}
      >
        {baseline.trend_rates.map((entry) => (
          <tr key={entry.row} className="border-b last:border-0">
            <RowHead>{TREND_ROW_LABEL[entry.row]}</RowHead>
            <Cell>{REVIEW_TEXT.rateOf(entry.rate.days, entry.rate.observed_days)}</Cell>
          </tr>
        ))}
      </ReviewTable>

      <ReviewTable
        label={REVIEW_TEXT.baselineItemRates}
        columns={[REVIEW_TEXT.baselineItemRates, REVIEW_TEXT.scoreColumns.observedDays]}
      >
        {baseline.item_rates.map((entry) => (
          <tr key={entry.item} className="border-b last:border-0">
            <RowHead>{ITEM_LABEL[entry.item]}</RowHead>
            <Cell>{REVIEW_TEXT.rateOf(entry.rate.days, entry.rate.observed_days)}</Cell>
          </tr>
        ))}
      </ReviewTable>
    </ReviewCard>
  );
}
