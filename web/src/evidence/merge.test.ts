import { describe, expect, it } from 'vitest';

import type { DaySignalItem, DaySignals, SignalEvidence } from '@/api/types';
import { mergeItem, withRowCancelled } from '@/evidence/merge';

const CONVERSATION = '0199a2b3-c4d5-7e6f-8a9b-0c1d2e3f4a5b';

function row(over: Partial<SignalEvidence> & Pick<SignalEvidence, 'id'>): SignalEvidence {
  return {
    conversation_id: CONVERSATION,
    status: 'observed',
    explicitness: 'direct',
    evidence: '어젯밤엔 잘 못 잤어',
    cancelled: false,
    ...over,
  };
}

function item(rows: SignalEvidence[]): DaySignalItem {
  // 합치기 전의 status와 explicitness는 일부러 틀린 값을 넣어 둔다. 계산한 값으로 덮는지 보려는 것이다.
  return { item: 'sleep', status: 'not_mentioned', explicitness: 'none', rows };
}

describe('하루의 판단 합치기', () => {
  it('한 번이라도 관찰됐으면 관찰됨이고, 그 판단을 받치는 가장 분명한 근거를 명시성으로 쓴다', () => {
    const merged = mergeItem(
      item([
        row({ id: 'a', status: 'not_observed', explicitness: 'direct' }),
        row({ id: 'b', status: 'observed', explicitness: 'indirect' }),
      ]),
    );

    expect(merged.status).toBe('observed');
    // 직접 말한 근거는 "괜찮았다"는 판단에 붙은 것이다. 관찰됨을 받치는 근거는 미루어 본 것뿐이다.
    expect(merged.explicitness).toBe('indirect');
  });

  it('하루에 두 번 이야기했으면 두 대화의 판단을 함께 본다. 나중 대화가 이긴다는 규칙은 없다', () => {
    const merged = mergeItem(
      item([
        row({ id: 'a', status: 'not_observed', explicitness: 'direct' }),
        row({
          id: 'b',
          conversation_id: '0199a2b3-c4d5-7e6f-8a9b-0c1d2e3f4a5c',
          status: 'observed',
          explicitness: 'indirect',
        }),
      ]),
    );

    expect(merged.status).toBe('observed');
    expect(merged.explicitness).toBe('indirect');
  });

  it('관찰된 판단이 없고 괜찮았다는 판단만 있으면 관찰되지 않음이다', () => {
    const merged = mergeItem(
      item([
        row({ id: 'a', status: 'not_observed', explicitness: 'indirect' }),
        row({ id: 'b', status: 'not_observed', explicitness: 'direct' }),
      ]),
    );

    expect(merged.status).toBe('not_observed');
    expect(merged.explicitness).toBe('direct');
  });

  it('취소한 판단은 셈에서 뺀다', () => {
    const merged = mergeItem(
      item([
        row({ id: 'a', status: 'observed', explicitness: 'direct', cancelled: true }),
        row({ id: 'b', status: 'not_observed', explicitness: 'indirect' }),
      ]),
    );

    expect(merged.status).toBe('not_observed');
    expect(merged.explicitness).toBe('indirect');
  });

  it('남은 판단이 하나도 없으면 언급 없음이 되고 근거도 없다', () => {
    const merged = mergeItem(item([row({ id: 'a', cancelled: true })]));

    expect(merged.status).toBe('not_mentioned');
    expect(merged.explicitness).toBe('none');
  });

  it('이야기가 없었던 항목은 그대로 언급 없음이다', () => {
    const merged = mergeItem(
      item([row({ id: 'a', status: 'not_mentioned', explicitness: 'none', evidence: null })]),
    );

    expect(merged.status).toBe('not_mentioned');
    expect(merged.explicitness).toBe('none');
  });
});

describe('행 하나의 취소 여부 바꾸기', () => {
  const day: DaySignals = {
    date: '2026-09-20',
    analysed: true,
    items: [
      { ...item([row({ id: 'a' })]), status: 'observed', explicitness: 'direct' },
      {
        item: 'mood',
        status: 'observed',
        explicitness: 'direct',
        rows: [row({ id: 'b' })],
      },
    ],
  };

  it('그 행이 속한 항목의 한 줄까지 함께 다시 계산한다', () => {
    const next = withRowCancelled(day, 'a', true);

    expect(next.items[0]?.rows[0]?.cancelled).toBe(true);
    expect(next.items[0]?.status).toBe('not_mentioned');
    // 하루가 분석됐다는 사실은 취소로 바뀌지 않는다.
    expect(next.analysed).toBe(true);
  });

  it('다른 항목과 원래 값은 건드리지 않는다', () => {
    const next = withRowCancelled(day, 'a', true);

    expect(next.items[1]).toBe(day.items[1]);
    expect(day.items[0]?.rows[0]?.cancelled).toBe(false);
    expect(day.items[0]?.status).toBe('observed');
  });

  it('없는 행을 가리키면 아무것도 바뀌지 않는다', () => {
    const next = withRowCancelled(day, 'zzz', true);

    expect(next.items).toEqual(day.items);
  });
});
