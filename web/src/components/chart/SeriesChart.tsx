export interface SeriesPoint {
  /** 가로축의 값. 표에도 같은 글이 쓰인다. */
  label: string;
  value: number;
  /** 눈에 띄게 찍고 짧은 말을 붙일 점. 흐름이 바뀐 날에만 붙인다. */
  marker?: string;
  /** 이름표를 점 아래에 붙인다. 위에 두면 기준선이나 옆의 이름표와 겹치는 자리에 쓴다. */
  markerBelow?: boolean;
  /** 말은 붙이지 않고 점만 찍는다. 짚어 둘 날이 여럿이라 이름표가 서로 겹칠 때 쓴다. */
  dot?: boolean;
  /** 마우스를 올렸을 때 보여 줄 한 줄. 값을 이것으로만 읽게 두지 않는다(표가 같은 값을 적는다). */
  hint?: string;
}

interface SeriesChartProps {
  /** 화면 낭독기가 읽는 이 그림의 이름 */
  label: string;
  points: readonly SeriesPoint[];
  /** 세로축의 끝 */
  max: number;
  /** 세로축에 눈금과 숫자를 적을 자리 */
  yTicks: readonly number[];
  /** 값이 그날 하루 내내 유지되는 흐름은 계단으로 그린다. */
  step?: boolean;
  /** 값을 견주는 기준선. 넘으면 뜻이 달라지는 값에만 붙인다. */
  reference?: { value: number; label: string };
}

/**
 * 날 사이의 간격. 날이 적으면 넓혀서 카드 안이 허전하지 않게 하고, 많아지면 좁히되 24px 아래로는 줄이지 않는다.
 * 그보다 좁아지면 점과 점이 붙어 흐름이 뭉개진다.
 */
const MIN_GAP = 24;
const MAX_GAP = 72;
const TARGET_PLOT_WIDTH = 520;
/**
 * 점이 둘뿐이어도 그림이 이 너비는 차지한다.
 *
 * 간격만으로 폭을 정하면 점 둘일 때 그림이 카드 왼쪽 구석의 작은 토막이 되고, 기준선과 눈금선도 함께 짧아져서
 * 그리다 만 그래프처럼 읽힌다. 폭을 먼저 정하고 점 사이 간격을 그 폭에서 되계산한다.
 */
const MIN_PLOT_WIDTH = 420;
const PAD_LEFT = 32;
/** 기준선의 이름표가 놓일 오른쪽 여백. 기준선이 없으면 그만큼 비워 두지 않는다. */
const PAD_RIGHT_WITH_REFERENCE = 86;
const PAD_RIGHT = 16;
const PAD_TOP = 20;
const PAD_BOTTOM = 26;
const PLOT_HEIGHT = 128;

/**
 * 값 하나가 날마다 어떻게 변했는지 그리는 작은 꺾은선(또는 계단).
 *
 * 줄이 하나뿐이라 색이 무엇을 가리키는지 알려 줄 범례가 필요하지 않다. 제목이 그 일을 한다.
 * 값은 언제나 옆의 표에도 있다. 그림을 읽지 못해도, 마우스를 올리지 못해도 값에 닿을 수 있어야 한다.
 */
export function SeriesChart({
  label,
  points,
  max,
  yTicks,
  step = false,
  reference,
}: SeriesChartProps) {
  if (points.length === 0) return null;

  const span = points.length - 1;
  const gap = span < 1 ? MAX_GAP : Math.min(MAX_GAP, Math.max(MIN_GAP, TARGET_PLOT_WIDTH / span));
  const plotWidth = Math.max(MIN_PLOT_WIDTH, span * gap);
  // 폭을 넓혀 잡았으면 점 사이도 그만큼 벌어진다. 점을 왼쪽에 모아 두고 오른쪽을 비워 두지 않는다.
  const spacing = span < 1 ? plotWidth : plotWidth / span;
  // 값이 뜨는 자리. 점 사이가 아주 벌어졌을 때 화면의 절반이 한 점의 자리가 되지 않게 묶어 둔다.
  const hit = Math.max(MIN_GAP, Math.min(spacing, MAX_GAP));
  const width =
    PAD_LEFT + plotWidth + (reference === undefined ? PAD_RIGHT : PAD_RIGHT_WITH_REFERENCE);
  const height = PAD_TOP + PLOT_HEIGHT + PAD_BOTTOM;
  const baseline = PAD_TOP + PLOT_HEIGHT;

  const x = (index: number) => PAD_LEFT + (points.length === 1 ? plotWidth / 2 : index * spacing);
  const y = (value: number) => {
    const ratio = max <= 0 ? 0 : Math.min(1, Math.max(0, value / max));
    return baseline - ratio * PLOT_HEIGHT;
  };

  // 계단은 값이 바뀌는 날의 앞에서 꺾는다. 그날 하루 내내 그 값이었다는 뜻이 되어야 한다.
  const line = points
    .flatMap((point, index) =>
      step && index > 0
        ? [`${x(index)},${y(points[index - 1]!.value)}`, `${x(index)},${y(point.value)}`]
        : [`${x(index)},${y(point.value)}`],
    )
    .join(' ');

  const first = points[0]!;
  const last = points[points.length - 1]!;

  return (
    // 폰에서는 날이 쌓이면 가로로 넘친다. 글자를 줄여 억지로 맞추지 않고 이 그림만 옆으로 밀어 본다.
    <div className="-mx-1 overflow-x-auto px-1">
      <svg
        role="img"
        aria-label={label}
        viewBox={`0 0 ${width} ${height}`}
        /*
          넓은 화면에서는 카드 폭에 맞춰 그림이 함께 커진다. 프로젝터로 띄우면 점과 글자가 같이 커져야 뒤에서도 읽힌다.
          좁은 화면에서는 이 최소 폭을 지키고 옆으로 밀어 본다. 억지로 줄이면 눈금의 글자가 6px이 된다.
          두 배까지만 키운다. 아주 큰 화면에서 선 두께와 글자가 우스꽝스럽게 굵어지지 않게 한다.
        */
        style={{ minWidth: width, maxWidth: 2 * width }}
        className="h-auto w-full text-muted-foreground"
      >
        {yTicks.map((tick) => (
          <g key={tick}>
            <line
              x1={PAD_LEFT}
              x2={PAD_LEFT + plotWidth}
              y1={y(tick)}
              y2={y(tick)}
              className="stroke-border"
              strokeWidth={1}
            />
            <text
              x={PAD_LEFT - 6}
              y={y(tick)}
              textAnchor="end"
              dominantBaseline="middle"
              className="fill-muted-foreground text-[11px] tabular-nums"
            >
              {tick}
            </text>
          </g>
        ))}

        {reference !== undefined && reference.value <= max && (
          <g>
            <line
              x1={PAD_LEFT}
              x2={PAD_LEFT + plotWidth}
              y1={y(reference.value)}
              y2={y(reference.value)}
              className="stroke-foreground"
              strokeWidth={1}
            />
            <text
              x={PAD_LEFT + plotWidth + 6}
              y={y(reference.value)}
              dominantBaseline="middle"
              className="fill-foreground text-[11px] font-semibold"
            >
              {reference.label}
            </text>
          </g>
        )}

        <polyline
          points={line}
          fill="none"
          className="stroke-primary"
          strokeWidth={2}
          strokeLinecap="round"
          strokeLinejoin="round"
        />

        {points.map((point, index) => (
          <g key={`${point.label}-${index}`}>
            {/* 점보다 넓은 자리를 잡아 두어 마우스를 정확히 올리지 않아도 값이 뜬다. */}
            <rect
              x={x(index) - hit / 2}
              y={PAD_TOP}
              width={hit}
              height={PLOT_HEIGHT}
              fill="transparent"
            >
              <title>{point.hint ?? `${point.label} ${point.value}`}</title>
            </rect>
            {(point.marker !== undefined || point.dot === true || index === points.length - 1) && (
              <>
                {/* 바탕색 테두리를 둘러 두면 점이 선과 겹쳐도 또렷하다. */}
                <circle
                  cx={x(index)}
                  cy={y(point.value)}
                  r={4.5}
                  className="fill-primary stroke-card"
                  strokeWidth={2}
                />
                {point.marker !== undefined && (
                  <text
                    x={x(index)}
                    y={y(point.value) + (point.markerBelow === true ? 18 : -10)}
                    textAnchor="middle"
                    className="fill-foreground text-[11px] font-semibold"
                  >
                    {point.marker}
                  </text>
                )}
              </>
            )}
          </g>
        ))}

        <text
          x={PAD_LEFT}
          y={height - 8}
          textAnchor="start"
          className="fill-muted-foreground text-[11px]"
        >
          {first.label}
        </text>
        {points.length > 1 && (
          <text
            x={PAD_LEFT + plotWidth}
            y={height - 8}
            textAnchor="end"
            className="fill-muted-foreground text-[11px]"
          >
            {last.label}
          </text>
        )}
      </svg>
    </div>
  );
}
