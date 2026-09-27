import { useEffect, useId, useRef } from 'react';
import { Link } from 'react-router';

import type { TrendMark, TrendRow } from '@/api/types';
import { TREND_MARK_ORDER, TREND_TEXT } from '@/content/trendText';
import { formatRecordDateShort } from '@/lib/recordDate';
import { dayOfMonth, isWeekend } from '@/trend/days';
import { cn } from '@/lib/utils';

/**
 * 표시 넷을 색이 아니라 모양과 크기로 가른다.
 * 찬 점 · 빈 점(테두리만) · 작은 점 · 빈칸이라서, 색을 구분하지 못하는 사람도 화면을 흑백으로 보는 사람도 읽을 수 있다.
 */
const MARK_CLASS = {
  observed: 'size-2.5 rounded-full bg-primary',
  not_observed: 'size-2.5 rounded-full border-2 border-input',
  not_mentioned: 'size-1 rounded-full bg-muted-foreground',
  no_conversation: '',
} satisfies Record<TrendMark, string>;

/** 점 하나. 뜻은 옆의 글이 말하므로 그림은 화면 낭독기에 알리지 않는다. */
export function TrendDot({ mark }: { mark: TrendMark }) {
  if (mark === 'no_conversation') return null;
  return <span aria-hidden="true" className={MARK_CLASS[mark]} />;
}

interface DotCalendarProps {
  /** 서버가 보낸 순서(기분, 수면, 에너지) 그대로 그린다. */
  rows: readonly TrendRow[];
  /**
   * 평소와 견주는 말을 붙일지. 기록이 모자란 동안에는 일수만 보여 준다.
   * 견줄 수 없는 까닭은 달력이 아니라 그 위의 안내가 말한다.
   */
  withComparison: boolean;
  /** 날짜를 누르면 갈 곳 */
  dayHref: (date: string) => string;
}

/**
 * 날마다 한 칸, 줄마다 한 가지 이야기인 점 달력.
 *
 * 표로 짠 까닭: 가로축(날짜)과 세로축(이야기)이 머리글로 이어져 있어야 화면 낭독기가 칸 하나를 읽을 때
 * "기분, 9월 27일, 신호가 보인 날"로 읽어 준다. 그림을 따로 표로 옮겨 적지 않아도 이 표가 곧 표다.
 */
export function DotCalendar({ rows, withComparison, dayHref }: DotCalendarProps) {
  const scroller = useRef<HTMLDivElement>(null);
  const captionId = useId();
  const dates = rows[0]?.cells.map((cell) => cell.date) ?? [];

  // 좁은 폰에서는 열이 24px을 지키느라 달력이 가로로 넘친다. 그때 먼저 보여야 하는 것은 오늘 쪽 끝이다.
  // 열이 다 들어가는 너비에서는 스크롤할 자리가 없어 아무 일도 일어나지 않는다.
  useEffect(() => {
    const element = scroller.current;
    if (element !== null) element.scrollLeft = element.scrollWidth;
  }, [dates.length]);

  if (dates.length === 0) return null;

  // 줄 길이가 어긋나도 열이 밀리지 않게 날짜로 찾는다.
  const marks = new Map(
    rows.map((row) => [row.row, new Map(row.cells.map((cell) => [cell.date, cell.mark]))]),
  );
  const talked = (date: string) =>
    rows.some((row) => marks.get(row.row)?.get(date) !== 'no_conversation');

  return (
    <div className="flex flex-col gap-3">
      {/*
        읽는 법은 표 밖에 둔다. caption으로 두면 표와 너비를 함께 쓰기 때문에, 달력이 옆으로 밀릴 때
        읽는 법도 같이 밀려 나간다. aria-describedby로 이어 두면 화면 낭독기에는 그대로 표의 설명이다.
      */}
      <p id={captionId} className="text-sm text-muted-foreground">
        {TREND_TEXT.calendarCaption}
      </p>
      {/*
        relative를 주는 까닭: 안의 sr-only 글은 position:absolute라서, 기준이 될 상자가 없으면 문서를 기준으로
        제자리 좌표를 잡는다. 그러면 옆으로 밀린 칸의 숨은 글이 문서 너비를 넘겨 쪽 전체에 가로 스크롤을 만든다.
      */}
      <div ref={scroller} className="relative -mx-1 overflow-x-auto px-1">
        {/*
          가장 좁은 너비는 줄 이름 자리 3.5rem에 날짜 열 열넷 × 1.5rem을 더한 24.5rem이다.
          열이 1.5rem보다 좁아지면 날짜를 누르는 자리를 손가락으로 겨냥하기 어려워진다.
          화면이 그보다 좁으면 이 칸 안에서만 가로로 밀린다. 쪽 전체는 밀리지 않는다.
          줄 이름은 sticky로 왼쪽에 붙여 둔다. 오른쪽 끝으로 밀어 두었을 때에도 어느 줄인지 보여야 한다.
          border-separate를 함께 주는 까닭: 칸을 합치는 기본 방식(border-collapse)에서는 브라우저가 칸의 sticky를
          제대로 잡지 못하는 일이 있다. 이 표에는 칸 테두리가 없어서 보이는 모습은 달라지지 않는다.
        */}
        <table
          aria-label={TREND_TEXT.calendarLabel}
          aria-describedby={captionId}
          className="w-full min-w-98 table-fixed border-separate border-spacing-0"
        >
          <colgroup>
            <col className="w-14" />
          </colgroup>
          <thead>
            <tr>
              <th scope="col" className="sr-only">
                {TREND_TEXT.dayColumn}
              </th>
              {dates.map((date) => (
                <th scope="col" key={date} className="p-0 align-bottom font-normal">
                  {talked(date) ? (
                    <Link
                      to={dayHref(date)}
                      className={cn(
                        'flex min-h-touch flex-col justify-end rounded-md pb-1 text-center text-[11px] tabular-nums hover:bg-accent hover:text-accent-foreground',
                        isWeekend(date) ? 'text-foreground' : 'text-muted-foreground',
                      )}
                    >
                      <span className="sr-only">{formatRecordDateShort(date)}</span>
                      <span aria-hidden="true">{dayOfMonth(date)}</span>
                    </Link>
                  ) : (
                    <span className="flex min-h-touch flex-col justify-end pb-1 text-center text-[11px] text-muted-foreground tabular-nums">
                      <span className="sr-only">{formatRecordDateShort(date)}</span>
                      <span aria-hidden="true">{dayOfMonth(date)}</span>
                    </span>
                  )}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.row}>
                <th
                  scope="row"
                  className="sticky left-0 z-10 bg-card py-1 pr-2 text-left align-middle text-sm font-semibold whitespace-nowrap"
                >
                  {TREND_TEXT.rowLabel[row.row]}
                </th>
                {dates.map((date) => {
                  const mark = marks.get(row.row)?.get(date) ?? 'no_conversation';
                  // 점이 눈에 띄는 자리라 사람들이 점을 누른다. 누르면 날짜 머리글과 같은 곳으로 가게 둔다.
                  const reading = `${TREND_TEXT.rowLabel[row.row]} ${formatRecordDateShort(date)} ${TREND_TEXT.mark[mark]}`;
                  return (
                    <td key={date} className="p-0">
                      {talked(date) ? (
                        <Link
                          to={dayHref(date)}
                          className="flex h-9 items-center justify-center rounded-md hover:bg-accent"
                        >
                          <TrendDot mark={mark} />
                          <span className="sr-only">{reading}</span>
                        </Link>
                      ) : (
                        <span className="flex h-9 items-center justify-center">
                          <TrendDot mark={mark} />
                          <span className="sr-only">{TREND_TEXT.mark[mark]}</span>
                        </span>
                      )}
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/*
        일수와 견주는 말은 스크롤러 밖에 둔다. 표 안에 두면 달력이 옆으로 밀릴 때 문장의 앞부분이 함께 밀려 나가고,
        폰에서는 "예요. 평소와 비슷해요."처럼 뒤쪽만 남는다. 이 화면이 내놓는 숫자가 그 문장에 있다.
      */}
      <ul className="flex flex-col gap-4">
        {rows.map((row) => {
          const comparison = withComparison ? TREND_TEXT.comparison[row.comparison] : '';
          return (
            <li key={row.row} className="flex flex-col gap-0.5">
              <p className="text-sm font-semibold">{TREND_TEXT.rowLabel[row.row]}</p>
              {/*
                이야기한 날이 하나도 없으면 셀 것이 없다. "0일 중 0일이에요"는 셈이 아니라 고장처럼 읽힌다.
                처음 온 사람에게 지금 할 수 있는 일은 달력 위의 안내가 말한다.
              */}
              {row.window.days > 0 && (
                <p className="leading-relaxed">
                  {TREND_TEXT.windowCount(row.window.days, row.window.observed_days)}
                  {comparison !== '' && ` ${comparison}`}
                </p>
              )}
              {withComparison && row.usual != null && (
                <p className="text-sm text-muted-foreground">
                  {TREND_TEXT.usualCount(row.usual.days, row.usual.observed_days)}
                </p>
              )}
              <p className="text-sm text-muted-foreground">{TREND_TEXT.rowNote[row.row]}</p>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

/** 점 읽는 법. 점의 뜻을 색이 아닌 모양으로 적어 둔다. */
export function DotLegend() {
  return (
    <div className="flex flex-col gap-2">
      <h2 className="text-sm font-semibold">{TREND_TEXT.legendTitle}</h2>
      <ul className="flex flex-col gap-1.5">
        {TREND_MARK_ORDER.map((mark) => (
          <li key={mark} className="flex items-center gap-2 text-sm text-muted-foreground">
            <span className="flex size-4 items-center justify-center">
              {mark === 'no_conversation' ? (
                // 빈칸에도 자리가 있다는 것을 보여 주려고 칸만 그린다.
                // 테두리 기본색은 카드 바탕과 너무 가까워 어두운 화면에서 보이지 않는다. 글자색을 옅게 써서 대비를 지킨다.
                <span
                  aria-hidden="true"
                  className="size-2.5 rounded-sm border border-dashed border-muted-foreground/70"
                />
              ) : (
                <TrendDot mark={mark} />
              )}
            </span>
            {TREND_TEXT.mark[mark]}
          </li>
        ))}
      </ul>
    </div>
  );
}
