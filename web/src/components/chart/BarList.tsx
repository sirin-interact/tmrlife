export interface BarDatum {
  key: string;
  label: string;
  /** 막대의 길이를 정하는 값 */
  value: number;
  /** 막대 끝에 적는 글. 없으면 값을 그대로 적는다. */
  valueText?: string;
  /** 막대 아래에 붙이는 짧은 설명 */
  note?: string;
}

interface BarListProps {
  /** 화면 낭독기가 읽는 이 그림의 이름 */
  label: string;
  data: readonly BarDatum[];
  /** 가로축의 끝. 막대는 모두 같은 자에 견준다 */
  max: number;
}

/**
 * 한 줄에 하나씩 눕힌 막대.
 *
 * 항목 이름이 한글이라 세로 막대의 이름표가 서로 겹친다. 눕히면 이름을 그대로 읽을 수 있다.
 * 막대는 모두 같은 색이다. 길이가 이미 크기를 말하고 있어서, 색을 값에 따라 바꾸면 같은 것을 두 번 적는 셈이다.
 */
export function BarList({ label, data, max }: BarListProps) {
  return (
    <ul aria-label={label} className="flex flex-col gap-3">
      {data.map((datum) => {
        const filled = max <= 0 ? 0 : Math.min(1, Math.max(0, datum.value / max));
        return (
          <li key={datum.key} className="flex flex-col gap-1">
            <div className="flex items-center gap-3">
              <span className="w-18 shrink-0 text-sm">{datum.label}</span>
              {/* 막대의 왼쪽은 0이라는 기준선이므로 각지게 두고, 값이 닿은 끝만 둥글린다. */}
              <span aria-hidden="true" className="h-2.5 min-w-0 flex-1 rounded-sm bg-primary/15">
                <span
                  className="block h-2.5 rounded-r-[4px] bg-primary"
                  style={{ width: `max(2px, ${(filled * 100).toFixed(1)}%)` }}
                />
              </span>
              <span className="w-10 shrink-0 text-right text-sm font-semibold tabular-nums">
                {datum.valueText ?? datum.value}
              </span>
            </div>
            {/* 이름표 자리(w-18)와 그 뒤의 간격(gap-3)을 더한 만큼 들여써서 설명이 막대 아래에 놓인다. */}
            {datum.note !== undefined && (
              <p className="pl-21 text-xs text-muted-foreground">{datum.note}</p>
            )}
          </li>
        );
      })}
    </ul>
  );
}
