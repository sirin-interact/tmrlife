import type { ReviewParams } from '@/api/types';
import { formatDecimal } from '@/components/chart/format';
import { Fact, FactList, ReviewCard } from '@/components/review/ReviewCard';
import { REVIEW_TEXT } from '@/content/reviewText';

const joinDays = (values: readonly number[]) => values.map((value) => `${value}일`).join(' · ');
const joinScores = (values: readonly number[]) => values.map((value) => `${value}점`).join(' · ');

/** 이 계산에 쓴 조정 값. 화면이 경계를 코드에 박아 두지 않았다는 사실을 눈으로 확인할 수 있게 둔다. */
export function ParamsSection({ params }: { params: ReviewParams }) {
  const label = REVIEW_TEXT.paramsLabel;

  return (
    <ReviewCard title={REVIEW_TEXT.paramsTitle}>
      <p className="text-sm text-muted-foreground">{REVIEW_TEXT.paramsNote}</p>
      <FactList>
        <Fact label={label.windowDays}>{REVIEW_TEXT.days(params.window_days)}</Fact>
        <Fact label={label.minConversationDays}>
          {REVIEW_TEXT.days(params.min_conversation_days)}
        </Fact>
        <Fact label={label.itemScoreMinDays}>{joinDays(params.item_score_min_days)}</Fact>
        <Fact label={label.bandMinScores}>{joinScores(params.band_min_scores)}</Fact>
        <Fact label={label.confidenceMediumMin}>{formatDecimal(params.confidence_medium_min)}</Fact>
        <Fact label={label.confidenceHighMin}>{formatDecimal(params.confidence_high_min)}</Fact>
        <Fact label={label.baselineWindowDays}>
          {REVIEW_TEXT.days(params.baseline_window_days)}
        </Fact>
        <Fact label={label.baselineMinConversationDays}>
          {REVIEW_TEXT.days(params.baseline_min_conversation_days)}
        </Fact>
        <Fact label={label.cusumK}>{formatDecimal(params.cusum_k)}</Fact>
        <Fact label={label.cusumH}>{formatDecimal(params.cusum_h)}</Fact>
        <Fact label={label.cusumMaxStep}>
          {params.cusum_max_step === 0
            ? REVIEW_TEXT.unlimited
            : formatDecimal(params.cusum_max_step)}
        </Fact>
        <Fact label={label.cusumMaxS}>
          {params.cusum_max_s === 0
            ? REVIEW_TEXT.unlimited
            : `${formatDecimal(params.cusum_max_s)}배`}
        </Fact>
        <Fact label={label.stageMinScores}>{joinScores(params.stage_min_scores)}</Fact>
        <Fact label={label.sustainedStage2Days}>
          {REVIEW_TEXT.days(params.sustained_stage2_days)}
        </Fact>
        <Fact label={label.trendMinDifferencePercent}>
          {REVIEW_TEXT.percent(params.trend_min_difference_percent)}
        </Fact>
      </FactList>
    </ReviewCard>
  );
}
