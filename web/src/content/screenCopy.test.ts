import { describe, expect, it } from 'vitest';

import type { SignalItem, SignalStatus } from '@/api/types';
import { EVIDENCE_TEXT, itemLabel, judgementLabel } from '@/content/evidenceText';
import {
  BAND_LABEL,
  CONFIDENCE_COMPONENT_LABEL,
  CONFIDENCE_COMPONENT_NOTE,
  CONFIDENCE_LEVEL_LABEL,
  CONFIDENCE_LIMITING_SENTENCE,
  ITEM_LABEL,
  REVIEW_TEXT,
  STAGE_LABEL,
  STAGE_REASON_LABEL,
  TREND_ROW_LABEL,
} from '@/content/reviewText';
import { ENDED_TEXT, NOTICE_TEXT, PHASE_TEXT, TALK_TEXT } from '@/content/talkText';
import { TREND_TEXT } from '@/content/trendText';
import { AVOIDED_WORDS, DENIABLE_WORDS, DENIAL_SENTENCES, stringsIn } from '@/test/avoidedWords';

const ITEMS: readonly SignalItem[] = [
  'interest',
  'mood',
  'sleep',
  'fatigue',
  'appetite',
  'self_blame',
  'concentration',
  'psychomotor',
];
const STATUSES: readonly SignalStatus[] = ['observed', 'not_observed', 'not_mentioned'];

/** 대화·추세·근거·내부 확인 화면의 문구 전부. 새 문구를 담은 파일이 생기면 여기에 더한다. */
const SCREEN_COPY: Record<string, unknown> = {
  대화: [TALK_TEXT, PHASE_TEXT, NOTICE_TEXT, ENDED_TEXT],
  추세: TREND_TEXT,
  근거: [
    EVIDENCE_TEXT,
    ITEMS.map(itemLabel),
    ITEMS.flatMap((item) => STATUSES.map((status) => judgementLabel(item, status))),
  ],
  내부: [
    REVIEW_TEXT,
    ITEM_LABEL,
    TREND_ROW_LABEL,
    BAND_LABEL,
    CONFIDENCE_LEVEL_LABEL,
    CONFIDENCE_COMPONENT_LABEL,
    CONFIDENCE_COMPONENT_NOTE,
    CONFIDENCE_LIMITING_SENTENCE,
    STAGE_LABEL,
    STAGE_REASON_LABEL,
  ],
};

describe('화면 문구', () => {
  it.each(Object.entries(SCREEN_COPY))('%s 화면의 문구를 모아 볼 수 있다', (_screen, copy) => {
    // 모으지 못하면 아래 검사가 조용히 통과한다. 검사가 실제로 무언가를 읽고 있는지부터 확인한다.
    expect(stringsIn(copy).length).toBeGreaterThan(10);
  });

  it.each(AVOIDED_WORDS)('"%s"이라는 말을 쓰지 않는다', (word) => {
    // 검사나 진단이 아니라는 사실은 화면에서 먼저 말해야 하는 것이라, 그것을 부정하는 문장에서만 그 말을 쓴다.
    const deniable = DENIABLE_WORDS.includes(word);

    for (const [screen, copy] of Object.entries(SCREEN_COPY)) {
      for (const text of stringsIn(copy)) {
        if (deniable && DENIAL_SENTENCES.includes(text)) continue;
        expect(text, screen).not.toContain(word);
      }
    }
  });

  it('부정하는 문장은 실제로 화면 문구에 있다', () => {
    // 문장을 고치면 허락도 함께 다시 생각하게 하려고, 목록에 적힌 문장이 남아 있는지 본다.
    const all = Object.values(SCREEN_COPY).flatMap(stringsIn);

    for (const sentence of DENIAL_SENTENCES) {
      expect(all, sentence).toContain(sentence);
    }
  });
});
