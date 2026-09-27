import type { ReactNode } from 'react';

import { REVIEW_TEXT } from '@/content/reviewText';
import { cn } from '@/lib/utils';

interface ReviewCardProps {
  title: string;
  /** 제목 바로 아래의 한 줄. 프로젝터로 볼 때 이 줄만 읽어도 결론이 보이게 둔다. */
  headline?: ReactNode;
  children: ReactNode;
}

export function ReviewCard({ title, headline, children }: ReviewCardProps) {
  return (
    // 이름을 붙인 영역이라 화면 낭독기가 "추정 점수 영역"처럼 묶음을 건너뛰며 읽을 수 있다. 화면이 길어서 더 요긴하다.
    <section
      aria-label={title}
      className="flex flex-col gap-4 rounded-2xl border bg-card px-5 py-6"
    >
      <div className="flex flex-col gap-1">
        <h2 className="text-lg leading-snug font-semibold">{title}</h2>
        {headline}
      </div>
      {children}
    </section>
  );
}

/** 이름과 값이 짝지어진 사실들. 값은 오른쪽에 모여서 눈으로 훑기 쉽다. */
export function FactList({ children }: { children: ReactNode }) {
  return <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-sm">{children}</dl>;
}

export function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="text-right tabular-nums">{children}</dd>
    </>
  );
}

/**
 * 참·거짓을 글로 적은 알약.
 *
 * 색만으로 켜짐과 꺼짐을 가리지 않는다. 안에 적힌 글이 언제나 상태를 말하고, 켜진 것은 테두리와 바탕이 함께 달라진다.
 */
export function Flag({ on, label }: { on: boolean; label?: string }) {
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-semibold',
        on ? 'border-primary bg-accent text-accent-foreground' : 'text-muted-foreground',
      )}
    >
      {label ?? (on ? REVIEW_TEXT.yes : REVIEW_TEXT.no)}
    </span>
  );
}

interface ReviewTableProps {
  label: string;
  columns: readonly string[];
  /** 숫자가 아닌 첫 칸만 왼쪽에 붙이고 나머지는 오른쪽에 맞춘다. */
  children: ReactNode;
  /** 날이 많이 쌓인 표는 카드 안에서만 스크롤한다. */
  scroll?: boolean;
}

export function ReviewTable({ label, columns, children, scroll = false }: ReviewTableProps) {
  return (
    <div className={cn('-mx-1 overflow-x-auto px-1', scroll && 'max-h-96 overflow-y-auto')}>
      {/* 넓은 화면에서는 글자를 한 단계 키운다. 이 표들은 강의실 뒤쪽에서 프로젝터로 읽히는 자리에 놓인다. */}
      <table aria-label={label} className="w-full min-w-max text-sm lg:text-[0.9375rem]">
        <thead>
          <tr className="border-b">
            {columns.map((column, index) => (
              <th
                key={column}
                scope="col"
                className={cn(
                  'py-1.5 font-semibold whitespace-nowrap',
                  index === 0 ? 'pr-3 text-left' : 'px-2 text-right',
                )}
              >
                {column}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}

/** 표의 첫 칸. 그 줄이 무엇에 대한 줄인지 말하므로 머리글로 둔다. */
export function RowHead({ children }: { children: ReactNode }) {
  return (
    <th scope="row" className="py-1.5 pr-3 text-left font-normal whitespace-nowrap">
      {children}
    </th>
  );
}

export function Cell({ children, className }: { children?: ReactNode; className?: string }) {
  return <td className={cn('px-2 py-1.5 text-right tabular-nums', className)}>{children}</td>;
}
