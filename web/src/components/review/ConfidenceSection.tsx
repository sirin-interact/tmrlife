import type { ReviewConfidence, ReviewParams } from '@/api/types';
import { formatRatio } from '@/components/chart/format';
import { Meter } from '@/components/chart/Meter';
import { Fact, FactList, ReviewCard } from '@/components/review/ReviewCard';
import {
  CONFIDENCE_COMPONENT_LABEL,
  CONFIDENCE_COMPONENT_NOTE,
  CONFIDENCE_LEVEL_LABEL,
  CONFIDENCE_LIMITING_SENTENCE,
  ITEM_LABEL,
  REVIEW_TEXT,
} from '@/content/reviewText';

const COMPONENTS = ['record_coverage', 'item_coverage', 'explicitness'] as const;

/**
 * 세 요소와 최종 값을 나란히 놓는다.
 *
 * 최종 값은 가장 작은 요소의 값이라서, 어느 요소가 최종 값을 정했는지 알약으로 짚어 둔다.
 * 그렇지 않으면 "세 값 중 왜 이 값인가"가 화면에서 사라진다.
 */
export function ConfidenceSection({
  confidence,
  params,
}: {
  confidence: ReviewConfidence;
  params: ReviewParams;
}) {
  const ratios = {
    record_coverage: confidence.record_coverage,
    item_coverage: confidence.item_coverage,
    explicitness: confidence.explicitness,
  };

  return (
    <ReviewCard
      title={REVIEW_TEXT.confidenceTitle}
      headline={
        <p className="flex flex-wrap items-baseline gap-x-3">
          <span className="text-2xl font-semibold">
            {REVIEW_TEXT.confidenceLevel(CONFIDENCE_LEVEL_LABEL[confidence.level])}
          </span>
          {confidence.insufficient ? (
            <span className="text-muted-foreground">{REVIEW_TEXT.confidenceInsufficient}</span>
          ) : (
            <>
              <span className="text-muted-foreground tabular-nums">
                {confidence.value.num} / {confidence.value.den}
              </span>
              {/* 판정 기준(0.4 · 0.7)과 단위를 맞춰 둔다. 분수만 적어 두면 판정이 맞는지 암산해야 확인된다. */}
              {confidence.value.den > 0 && (
                <span className="text-muted-foreground tabular-nums">
                  {formatRatio(confidence.value.num, confidence.value.den)}
                </span>
              )}
            </>
          )}
        </p>
      }
    >
      <p className="text-sm">
        {confidence.limiting === 'none'
          ? REVIEW_TEXT.confidenceLimitingNone
          : CONFIDENCE_LIMITING_SENTENCE[confidence.limiting]}
      </p>

      <div className="flex flex-col gap-4">
        {COMPONENTS.map((component) => (
          <Meter
            key={component}
            label={CONFIDENCE_COMPONENT_LABEL[component]}
            note={CONFIDENCE_COMPONENT_NOTE[component]}
            num={ratios[component].num}
            den={ratios[component].den}
            limiting={confidence.limiting === component}
            limitingLabel="가장 작음"
          />
        ))}
      </div>

      <p className="text-sm text-muted-foreground">{REVIEW_TEXT.confidenceMin}</p>
      <p className="text-sm text-muted-foreground">
        {REVIEW_TEXT.confidenceBounds(params.confidence_medium_min, params.confidence_high_min)}
      </p>

      <FactList>
        <Fact label={REVIEW_TEXT.confidenceConversationDays}>
          {REVIEW_TEXT.days(confidence.conversation_days)}
        </Fact>
        <Fact label={REVIEW_TEXT.confidenceMentionedItems}>{confidence.mentioned_items}</Fact>
        <Fact label={REVIEW_TEXT.confidenceObservedJudgements}>
          {confidence.observed_judgements}
        </Fact>
        <Fact label={REVIEW_TEXT.confidenceDirectJudgements}>{confidence.direct_judgements}</Fact>
        <Fact label={REVIEW_TEXT.confidenceMissingItems}>
          {confidence.missing_items.length === 0
            ? REVIEW_TEXT.confidenceMissingNone
            : confidence.missing_items.map((item) => ITEM_LABEL[item]).join(', ')}
        </Fact>
      </FactList>
    </ReviewCard>
  );
}
