import type { ConversationMode } from '@/api/types';

/** 사용자가 마지막으로 고른 대화 방식을 기억해 두는 자리. 편의일 뿐이라 없어도, 읽지 못해도 된다. */
const STORAGE_KEY = 'naeil.talk.mode';

/** 기억해 둔 방식. 아무것도 없으면 음성이다. 말로 하는 일기가 이 앱의 기본이다. */
export function readPreferredMode(): ConversationMode {
  try {
    return localStorage.getItem(STORAGE_KEY) === 'chat' ? 'chat' : 'voice';
  } catch {
    // 비공개 창이나 저장을 막은 브라우저. 기본값으로 간다.
    return 'voice';
  }
}

export function rememberPreferredMode(mode: ConversationMode): void {
  try {
    localStorage.setItem(STORAGE_KEY, mode);
  } catch {
    // 기억하지 못해도 대화에는 지장이 없다.
  }
}
