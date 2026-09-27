/**
 * 근거 화면(하루의 마음 신호와 그 근거)의 문구. 화면 코드에는 문구를 따로 적지 않는다.
 *
 * 이 화면은 "왜 그렇게 봤는지"를 사용자가 직접 확인하는 자리다. 그래서 판단은 짧은 우리말 한 줄로 적고,
 * 근거는 사용자가 한 말을 글자 하나 고치지 않고 그대로 따온다.
 */

import type { SignalExplicitness, SignalItem, SignalStatus } from '@/api/types';

interface ItemCopy {
  /** 항목 이름. 척도의 용어가 아니라 사용자가 자기 말로 떠올릴 수 있는 말로 적는다. */
  label: string;
  /** 그 신호가 있었던 날의 한 줄 */
  observed: string;
  /** 이야기가 나왔고 괜찮았던 날의 한 줄 */
  notObserved: string;
}

// 여덟 항목 모두 적는다. Record라서 항목이 늘면 컴파일이 멈춘다.
const ITEMS: Record<SignalItem, ItemCopy> = {
  interest: {
    label: '즐거움',
    observed: '즐거운 일이 줄었어요',
    notObserved: '즐거운 순간이 있었어요',
  },
  mood: { label: '기분', observed: '기분이 가라앉았어요', notObserved: '기분은 괜찮았어요' },
  sleep: { label: '잠', observed: '잠이 편하지 않았어요', notObserved: '잠은 잘 잤어요' },
  fatigue: { label: '기운', observed: '기운이 없었어요', notObserved: '기운은 괜찮았어요' },
  appetite: { label: '입맛', observed: '입맛이 평소와 달랐어요', notObserved: '잘 먹었어요' },
  self_blame: {
    label: '나를 보는 마음',
    observed: '스스로를 탓했어요',
    notObserved: '스스로를 탓하지 않았어요',
  },
  concentration: {
    label: '집중',
    observed: '집중하기 어려웠어요',
    notObserved: '집중은 잘 됐어요',
  },
  psychomotor: {
    label: '몸의 움직임',
    observed: '몸이 무겁거나 안절부절못했어요',
    notObserved: '몸은 편했어요',
  },
};

/** 이야기가 없었던 항목. 빠뜨린 것이 아니라 그날 나오지 않은 이야기라서 사과하듯 적지 않는다. */
export const NOT_MENTIONED_LABEL = '말하지 않음';

export function itemLabel(item: SignalItem): string {
  return ITEMS[item].label;
}

export function judgementLabel(item: SignalItem, status: SignalStatus): string {
  switch (status) {
    case 'observed':
      return ITEMS[item].observed;
    case 'not_observed':
      return ITEMS[item].notObserved;
    case 'not_mentioned':
      return NOT_MENTIONED_LABEL;
  }
}

// 언급 없음인 판단에는 근거가 없다. 붙일 말도 없어서 null이다.
const EXPLICITNESS: Record<SignalExplicitness, string | null> = {
  direct: '직접 말한 내용',
  indirect: '말에서 미루어 본 내용',
  none: null,
};

export function explicitnessLabel(explicitness: SignalExplicitness): string | null {
  return EXPLICITNESS[explicitness];
}

export const EVIDENCE_TEXT = {
  pageTitle: (date: string) => `${date}의 마음 신호 · 내일`,
  lead: '이야기 속에서 이렇게 읽었어요. 따온 문장은 그날 내가 한 말이에요.',
  loading: '마음 신호를 불러오고 있어요.',
  retry: '다시 불러오기',
  refresh: '다시 확인하기',
  refreshing: '확인하고 있어요',
  backToTrend: '변화 추세로',
  toDiary: '이 날의 일기 보기',
  toTalk: '오늘 이야기하기',

  itemsLabel: '이 날의 마음 신호',

  cancel: '이건 아니에요',
  cancelFor: (item: string) => `${item}: 이건 아니에요`,
  undo: '되돌리기',
  undoFor: (item: string) => `${item}: 되돌리기`,
  cancelledBadge: '빼 두었어요',
  cancelNote:
    '"이건 아니에요"를 누르면 그 신호는 변화 추세를 만드는 기록에서 빠져요. 눌렀다는 사실은 남겨 두어서, 내 말을 지나치게 읽은 곳이 없는지 나중에 되짚어 볼 수 있어요.',
  cancelledNote: '이 신호는 변화 추세를 만드는 기록에서 빠져 있어요.',
  cancelFailed: '지금은 바꾸지 못했어요. 잠시 뒤에 다시 눌러 주세요.',
  cancelGone: '이 기록이 이미 없어요. 화면을 다시 불러와 주세요.',

  noConversationTitle: '이 날은 이야기를 나누지 않았어요',
  noConversationBody: '이야기를 나눈 날에만 마음 신호가 남아요.',
  justTalkedHint: '방금 이야기를 마쳤다면 잠시 뒤에 여기에 나타나요.',
  pendingTitle: '이야기를 정리하고 있어요',
  pendingBody: '조금만 기다리면 이 자리에 나타나요. 화면은 저절로 다시 불러와요.',
  stalledTitle: '아직 정리되지 않았어요',
  stalledBody: '나눈 이야기와 일기는 그대로 있어요. 잠시 뒤에 다시 확인해 주세요.',
  disabledTitle: '마음 신호 읽기를 꺼 두셨어요',
  disabledBody:
    '다시 켜면 그 뒤에 나눈 이야기부터 기록돼요. 이미 나눈 이야기와 일기는 그대로 있어요.',
} as const;
