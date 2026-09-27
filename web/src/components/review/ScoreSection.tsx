import type { ReviewParams, ReviewScore } from '@/api/types';
import { BarList, type BarDatum } from '@/components/chart/BarList';
import { formatDecimal } from '@/components/chart/format';
import {
  Cell,
  Fact,
  FactList,
  ReviewCard,
  ReviewTable,
  RowHead,
} from '@/components/review/ReviewCard';
import { BAND_LABEL, ITEM_LABEL, REVIEW_TEXT } from '@/content/reviewText';
import { formatRecordDateShort } from '@/lib/recordDate';

/** 항목 하나가 받을 수 있는 가장 높은 점수. 여덟 항목의 합이 추정 점수다. */
const MAX_ITEM_POINTS = 3;

/**
 * 관찰된 날이 어떻게 항목 점수가 되고, 그 합이 어떤 구간에 드는지 한 화면에서 따라갈 수 있게 한다.
 *
 * 기록이 모자란 날에는 환산 일수와 점수와 합계를 그리지 않는다. 서버가 구하지 않은 값이라 0으로 보이는데,
 * 그 0을 "충분히 듣고 신호가 없었다"로 읽으면 뜻이 완전히 뒤집힌다.
 */
export function ScoreSection({ score, params }: { score: ReviewScore; params: ReviewParams }) {
  const maxTotal = score.items.length * MAX_ITEM_POINTS;

  const bars: BarDatum[] = score.items.map((item) => ({
    key: item.item,
    label: ITEM_LABEL[item.item],
    value: item.points,
    valueText: `${item.points}점`,
    note: `관찰 ${item.observed_days}일 → 환산 ${formatDecimal(item.converted_days)}일`,
  }));

  return (
    <ReviewCard
      title={REVIEW_TEXT.scoreTitle}
      headline={
        score.insufficient ? (
          <p className="font-semibold">{REVIEW_TEXT.scoreInsufficient}</p>
        ) : (
          <p className="flex flex-wrap items-baseline gap-x-3">
            {/* 이 화면에서 가장 큰 숫자 하나. 자릿수를 고르게 맞추지 않아 큰 글씨에서도 촘촘하게 읽힌다. */}
            <span className="text-5xl leading-none font-semibold">{score.total}</span>
            <span className="text-muted-foreground">
              / {maxTotal} · {BAND_LABEL[score.band]}
            </span>
          </p>
        )
      }
    >
      <FactList>
        <Fact label={REVIEW_TEXT.scoreWindowLabel}>
          <time dateTime={score.window_from}>{formatRecordDateShort(score.window_from)}</time> ~{' '}
          <time dateTime={score.window_to}>{formatRecordDateShort(score.window_to)}</time>
        </Fact>
        <Fact label={REVIEW_TEXT.confidenceConversationDays}>
          {REVIEW_TEXT.days(score.conversation_days)}
        </Fact>
      </FactList>

      {score.insufficient ? (
        <p className="text-sm text-muted-foreground">
          {REVIEW_TEXT.scoreInsufficientNote(params.min_conversation_days)}
        </p>
      ) : (
        <>
          <BarList label={REVIEW_TEXT.scoreChartLabel} data={bars} max={MAX_ITEM_POINTS} />
          <p className="text-sm text-muted-foreground">
            {REVIEW_TEXT.scoreItemBounds(params.item_score_min_days)}
          </p>
          <p className="text-sm text-muted-foreground">
            {REVIEW_TEXT.scoreBandBounds(params.band_min_scores)}
          </p>
        </>
      )}

      <ReviewTable
        label={REVIEW_TEXT.scoreItemsLabel}
        columns={
          score.insufficient
            ? [REVIEW_TEXT.scoreColumns.item, REVIEW_TEXT.scoreColumns.observedDays]
            : [
                REVIEW_TEXT.scoreColumns.item,
                REVIEW_TEXT.scoreColumns.observedDays,
                REVIEW_TEXT.scoreColumns.convertedDays,
                REVIEW_TEXT.scoreColumns.points,
              ]
        }
      >
        {score.items.map((item) => (
          <tr key={item.item} className="border-b last:border-0">
            <RowHead>{ITEM_LABEL[item.item]}</RowHead>
            <Cell>{item.observed_days}</Cell>
            {!score.insufficient && (
              <>
                <Cell>{formatDecimal(item.converted_days)}</Cell>
                <Cell className="font-semibold">{item.points}</Cell>
              </>
            )}
          </tr>
        ))}
        {!score.insufficient && (
          <tr>
            <RowHead>{REVIEW_TEXT.scoreTotal}</RowHead>
            <Cell />
            <Cell />
            <Cell className="font-semibold">{REVIEW_TEXT.scoreTotalOf(score.total, maxTotal)}</Cell>
          </tr>
        )}
      </ReviewTable>
    </ReviewCard>
  );
}
