import type { ReviewParams, ReviewStage } from '@/api/types';
import { SeriesChart, type SeriesPoint } from '@/components/chart/SeriesChart';
import { Cell, Flag, ReviewCard, ReviewTable, RowHead } from '@/components/review/ReviewCard';
import {
  CONFIDENCE_LEVEL_LABEL,
  REVIEW_TEXT,
  STAGE_LABEL,
  STAGE_REASON_LABEL,
} from '@/content/reviewText';
import { dayOfMonth } from '@/trend/days';
import { formatRecordDateShort } from '@/lib/recordDate';

const MAX_STAGE = 3;

/**
 * 첫 대화 날부터 기준일까지 하루씩 다시 돌린 개입 단계.
 *
 * 계단으로 그리는 까닭: 그 값은 그날 하루 내내의 단계다. 꺾은선으로 이으면 하루 사이에 단계가 조금씩
 * 올랐다는 뜻으로 읽힌다.
 *
 * 오르려던 만큼 오르지 못한 날은 점만 찍고 까닭은 표에 적는다. 그런 날이 여럿 이어지면 이름표가 서로 겹친다.
 */
export function StageSection({ stage, params }: { stage: ReviewStage; params: ReviewParams }) {
  const points: SeriesPoint[] = stage.series.map((point, index) => {
    const before = index === 0 ? null : stage.series[index - 1]!.stage;
    return {
      label: String(dayOfMonth(point.date)),
      value: point.stage,
      // 단계가 움직인 날에만 숫자를 적는다. 날마다 적으면 읽히지 않는다.
      marker: before !== null && before !== point.stage ? String(point.stage) : undefined,
      dot: point.held,
      hint: `${formatRecordDateShort(point.date)} · ${point.stage}단계${point.held ? ' · 오르지 못함' : ''}`,
    };
  });

  const held = stage.series.filter((point) => point.held);

  return (
    <ReviewCard
      title={REVIEW_TEXT.stageTitle}
      headline={
        <p className="text-2xl font-semibold">
          {REVIEW_TEXT.stageNow(stage.stage, STAGE_LABEL[stage.stage] ?? '')}
        </p>
      }
    >
      {stage.from != null && (
        <p className="text-sm text-muted-foreground">
          {REVIEW_TEXT.stageFrom(formatRecordDateShort(stage.from))}
        </p>
      )}
      <p className="text-sm text-muted-foreground">{REVIEW_TEXT.stageRules}</p>
      <p className="text-sm text-muted-foreground">
        {REVIEW_TEXT.stageParams(params.stage_min_scores, params.sustained_stage2_days)}
      </p>

      {points.length === 0 ? (
        <p className="text-muted-foreground">{REVIEW_TEXT.stageNoSeries}</p>
      ) : (
        <>
          <SeriesChart
            label={REVIEW_TEXT.stageChartLabel}
            points={points}
            max={MAX_STAGE}
            yTicks={[0, 1, 2, 3]}
            step
          />
          <p className="text-sm text-muted-foreground">
            {REVIEW_TEXT.stageHeld} {held.length === 0 ? REVIEW_TEXT.none : `${held.length}일`}
          </p>
          <ReviewTable
            label={REVIEW_TEXT.stageChartLabel}
            columns={[
              REVIEW_TEXT.stageColumns.date,
              REVIEW_TEXT.stageColumns.record,
              REVIEW_TEXT.stageColumns.score,
              REVIEW_TEXT.stageColumns.confidence,
              REVIEW_TEXT.stageColumns.detected,
              REVIEW_TEXT.stageColumns.raw,
              REVIEW_TEXT.stageColumns.stage,
              REVIEW_TEXT.stageColumns.reasons,
            ]}
            scroll
          >
            {stage.series.map((point) => (
              <tr key={point.date} className="border-b last:border-0">
                <RowHead>
                  <time dateTime={point.date}>{formatRecordDateShort(point.date)}</time>
                </RowHead>
                <Cell>
                  {point.has_record ? REVIEW_TEXT.stageHasRecord : REVIEW_TEXT.stageNoRecord}
                </Cell>
                {/* 기록 부족인 날의 점수는 서버가 구하지 않은 값이다. 0으로 적으면 "신호가 없었다"로 읽힌다. */}
                <Cell>{point.insufficient ? REVIEW_TEXT.none : point.score}</Cell>
                <Cell>{CONFIDENCE_LEVEL_LABEL[point.confidence]}</Cell>
                <Cell>
                  <Flag on={point.detected} />
                </Cell>
                <Cell>{point.raw}</Cell>
                <Cell className="font-semibold">
                  {point.stage}
                  {point.elevated_days > 0 && (
                    <span className="ml-1 text-xs font-normal text-muted-foreground">
                      {REVIEW_TEXT.stageElevatedDays(point.elevated_days)}
                    </span>
                  )}
                </Cell>
                <td className="px-2 py-1.5 text-left">
                  {point.reasons.length === 0 ? (
                    <span className="text-muted-foreground">—</span>
                  ) : (
                    <ul className="flex flex-col gap-0.5">
                      {point.reasons.map((reason) => (
                        <li key={reason} className="whitespace-nowrap">
                          {STAGE_REASON_LABEL[reason]}
                        </li>
                      ))}
                    </ul>
                  )}
                </td>
              </tr>
            ))}
          </ReviewTable>
        </>
      )}
    </ReviewCard>
  );
}
