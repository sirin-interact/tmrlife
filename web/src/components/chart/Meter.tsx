import { formatRatio } from '@/components/chart/format';
import { cn } from '@/lib/utils';

interface MeterProps {
  label: string;
  /** 그 값이 무엇을 나눈 것인지. 숫자만 보고 뜻을 짐작하지 않게 한다. */
  note?: string;
  /** 센 값 그대로의 분자와 분모. 약분하거나 소수로 바꾸지 않는다. */
  num: number;
  den: number;
  /** 이 요소가 최종 값을 정했다면 그 사실을 함께 적는다. */
  limiting?: boolean;
  limitingLabel?: string;
}

/**
 * 한계에 견준 비율 하나를 보여 준다.
 *
 * 채운 부분과 빈 부분이 같은 색의 짙고 옅은 단계라서, 색을 구분하지 못해도 어디까지 찼는지 읽힌다.
 * 값은 언제나 글로도 적는다. 그림만 보고 읽어야 하는 값은 두지 않는다.
 */
export function Meter({ label, note, num, den, limiting = false, limitingLabel }: MeterProps) {
  const filled = den === 0 ? 0 : Math.min(1, Math.max(0, num / den));

  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3">
        <span className={cn('text-sm font-semibold', limiting && 'text-foreground')}>
          {label}
          {limiting && limitingLabel !== undefined && (
            <span className="ml-2 rounded-full bg-accent px-2 py-0.5 text-xs font-semibold text-accent-foreground">
              {limitingLabel}
            </span>
          )}
        </span>
        <span className="flex items-baseline gap-x-1.5 text-sm tabular-nums">
          <span>
            {num} / {den}
          </span>
          {/* 이 비율을 판정할 때 쓰는 기준은 소수다. 같은 단위로도 적어 두어 암산할 일을 없앤다. */}
          {den > 0 && <span className="text-muted-foreground">{formatRatio(num, den)}</span>}
        </span>
      </div>
      {/* 값은 위의 글이 말한다. 그림은 거들기만 하므로 화면 낭독기에 따로 알리지 않는다. */}
      <div aria-hidden="true" className="h-2.5 w-full rounded-full bg-primary/20">
        <div
          className="h-2.5 rounded-full bg-primary"
          style={{ width: `${(filled * 100).toFixed(1)}%` }}
        />
      </div>
      {note !== undefined && <p className="text-xs text-muted-foreground">{note}</p>}
    </div>
  );
}
