import type { Ref } from 'react';

import { cn } from '@/lib/utils';

/** 구슬이 지금 무엇을 하고 있는지. 모양과 움직임은 talk.css의 .orb 규칙이 맡는다. */
export type OrbMode =
  /** 사용자의 말을 기다린다 */
  | 'idle'
  /** 마이크로 듣고 있다 */
  | 'listening'
  /** 답을 준비하고 있다 */
  | 'thinking'
  /** 답을 건네고 있다 */
  | 'speaking'
  /** 연결이 끊겼거나 대화가 끝났다 */
  | 'off';

/** 구슬을 눌렀을 때 하는 일. 있으면 구슬이 버튼이 된다. */
export interface OrbAction {
  label: string;
  onPress: () => void;
}

interface OrbProps {
  mode: OrbMode;
  action?: OrbAction | null;
  /** 소리 크기(--orb-level)를 리액트를 거치지 않고 바로 넣으려고 요소를 내준다. */
  ref?: Ref<HTMLElement>;
  className?: string;
}

/**
 * 대화 화면 가운데의 구슬. 이야기를 나누는 상대가 있다는 느낌을 주는 자리다.
 * 누를 일이 없을 때는 장식이라 화면 낭독기에 알리지 않는다. 무엇을 하고 있는지는 곁의 글(상태 줄)이 전한다.
 * 누를 일이 있을 때(이야기 시작, 다 말했어요, 잠깐 멈추기)는 버튼이 되고 그 뜻이 이름이 된다.
 */
export function Orb({ mode, action, ref, className }: OrbProps) {
  const layers = (
    <span className="orb__scene" aria-hidden="true">
      <span className="orb__ripple" />
      <span className="orb__ripple orb__ripple--two" />
      <span className="orb__body" />
    </span>
  );

  if (action) {
    return (
      <button
        ref={ref as Ref<HTMLButtonElement>}
        type="button"
        aria-label={action.label}
        data-mode={mode}
        onClick={action.onPress}
        className={cn('orb', className)}
      >
        {layers}
      </button>
    );
  }
  return (
    <div
      ref={ref as Ref<HTMLDivElement>}
      aria-hidden="true"
      data-mode={mode}
      className={cn('orb', className)}
    >
      {layers}
    </div>
  );
}
