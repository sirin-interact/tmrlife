import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { SeriesChart, type SeriesPoint } from '@/components/chart/SeriesChart';

function points(count: number): SeriesPoint[] {
  return Array.from({ length: count }, (_, index) => ({ label: String(index + 1), value: index }));
}

function chart(count: number, withReference = true) {
  render(
    <SeriesChart
      label="날마다의 누적값"
      points={points(count)}
      max={6}
      yTicks={[0, 6]}
      reference={withReference ? { value: 4, label: '한계값 4' } : undefined}
    />,
  );
  return screen.getByRole('img', { name: '날마다의 누적값' });
}

/** viewBox의 "0 0 너비 높이"에서 너비를 읽는다. */
function widthOf(svg: HTMLElement): number {
  return Number((svg.getAttribute('viewBox') ?? '').split(' ')[2]);
}

/** 꺾은선의 점을 가로 좌표만 뽑아서 읽는다. */
function xsOf(svg: HTMLElement): number[] {
  const line = svg.querySelector('polyline')?.getAttribute('points') ?? '';
  return line
    .split(' ')
    .filter((pair) => pair !== '')
    .map((pair) => Number(pair.split(',')[0]));
}

describe('꺾은선 그림', () => {
  it('점이 둘뿐이어도 그림이 카드 왼쪽의 작은 토막이 되지 않는다', () => {
    const svg = chart(2);

    // 그리는 자리의 폭은 적어도 420px이다. 왼쪽 눈금 자리 32px과 기준선 이름표 자리 86px을 더해 538px이 된다.
    expect(widthOf(svg)).toBe(538);
    // 점 둘이 그 폭의 양 끝에 놓인다. 왼쪽에 모아 두고 오른쪽을 비워 두지 않는다.
    expect(xsOf(svg)).toEqual([32, 452]);
  });

  it('기준선과 눈금선도 그 폭을 끝까지 쓴다', () => {
    const svg = chart(2);

    // 72px짜리 짧은 토막으로 떠 있으면 그리다 만 그래프처럼 읽힌다.
    const ends = [...svg.querySelectorAll('line')].map((line) => line.getAttribute('x2'));
    expect(new Set(ends)).toEqual(new Set(['452']));
  });

  it('점이 하나면 그 폭의 가운데에 놓는다', () => {
    const svg = chart(1);

    expect(widthOf(svg)).toBe(538);
    expect(xsOf(svg)).toEqual([242]);
  });

  it('날이 쌓이면 폭이 늘어나고 점 사이는 24px까지만 좁아진다', () => {
    const svg = chart(31, false);

    // 점 사이가 24px이면 30칸에 720px이고, 오른쪽 여백 16px을 더해 768px이 된다.
    expect(widthOf(svg)).toBe(768);
    expect(xsOf(svg).slice(0, 3)).toEqual([32, 56, 80]);
  });
});
