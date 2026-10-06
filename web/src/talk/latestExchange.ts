import type { ChatMessage } from '@/talk/conversationState';

export interface Exchange {
  user: ChatMessage | null;
  ai: ChatMessage | null;
}

/**
 * 지금 주고받는 한 마디.
 * 마지막 말이 사용자의 것이면(답을 기다리는 중) 그 말만, 내일의 답이면 바로 앞의 사용자 말과 함께 돌려준다.
 */
export function latestExchange(messages: readonly ChatMessage[]): Exchange {
  const last = messages.at(-1);
  if (last === undefined) return { user: null, ai: null };
  if (last.speaker === 'user') return { user: last, ai: null };
  const before = messages.at(-2);
  return { user: before?.speaker === 'user' ? before : null, ai: last };
}
