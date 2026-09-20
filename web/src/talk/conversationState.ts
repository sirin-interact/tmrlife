import type { Resource, WsUtterance } from '@/api/types';
import type { EndReason, ServerMessage } from '@/talk/messages';

/**
 * 대화 화면의 상태. 소켓도 타이머도 모르는 순수한 계산이다.
 * 무슨 일이 일어났는지(사건)를 받아 다음 상태를 돌려준다. 소켓을 열고 다시 보내는 일은 ConversationClient가 한다.
 */

export type ConnectionPhase =
  /** 처음 연결하는 중. 아직 보여 줄 대화가 없다. */
  | 'connecting'
  /** 서버가 ready로 답했다. 글을 보낼 수 있다. */
  | 'ready'
  /** 연결이 끊겨 다시 잇는 중. 지난 대화는 그대로 보인다. */
  | 'reconnecting'
  /** 여러 번 해 봐도 이어지지 않았다. 사용자가 다시 시도를 눌러야 한다. */
  | 'failed'
  /** 대화가 끝났다. */
  | 'ended';

export type Delivery = 'sending' | 'sent' | 'failed';

export interface ChatMessage {
  /** 화면 목록의 키. 서버가 순번을 주기 전에도 있어야 해서 글은 보낼 때 붙인 식별자로 만든다. */
  key: string;
  /** 대화 안에서의 순번. 서버가 받았다고 알려 오기 전에는 null이다. */
  seq: number | null;
  speaker: 'user' | 'ai';
  text: string;
  clientMessageId: string | null;
  delivery: Delivery;
}

/** 화면에 잠깐 알려야 하는 일 */
export type TalkNotice =
  /** 글을 보내지 못했다. 그 글에 다시 보내기 버튼이 붙는다. */
  | 'send_failed'
  /** 글이 너무 길다 */
  | 'too_long'
  /** 너무 자주 보냈다. 잠시 뒤에 알아서 다시 보낸다. */
  | 'slow_down'
  /** 쉬는 사이에 앞의 대화가 끝나서 새 대화로 이어졌다 */
  | 'new_conversation'
  /** 서버와 말이 맞지 않는다. 설치된 앱이 옛 판일 때 생긴다. */
  | 'protocol';

export interface EndedInfo {
  /** 'elsewhere'는 이 화면이 모르는 사이에(다른 기기, 오랜 무응답) 끝나 있었다는 뜻이다. */
  reason: EndReason | 'elsewhere';
  recordDate: string | null;
  /** 일기 초안을 만들고 있는지. 서버의 답을 받지 못하고 끝났으면 알 수 없다(null). */
  diaryExpected: boolean | null;
}

export interface ConversationState {
  phase: ConnectionPhase;
  conversationId: string | null;
  recordDate: string | null;
  messages: readonly ChatMessage[];
  /** 글을 보냈고 답을 기다리는 중인지. 기다리는 동안에는 다음 글을 보내지 않는다. */
  awaitingReply: boolean;
  /** 화면에 고정해 둔 도움 자원. 한 번 오면 대화가 끝난 뒤까지 남는다. */
  resources: readonly Resource[] | null;
  /** 끝내기를 확인했고 서버의 답을 기다리는 중인지 */
  ending: boolean;
  ended: EndedInfo | null;
  diaryReady: boolean;
  notice: TalkNotice | null;
  /** 방금 도착한 AI의 말. 화면 낭독기에 읽어 줄 것만 담는다. 이어가는 대화의 지난 발화는 담지 않는다. */
  arrived: { seq: number; text: string } | null;
}

export type ConversationEvent =
  | { type: 'connecting' }
  | { type: 'disconnected' }
  | { type: 'gave_up' }
  | { type: 'server'; message: ServerMessage }
  | { type: 'submitted'; clientMessageId: string; text: string }
  | { type: 'retried'; clientMessageId: string }
  | { type: 'send_gave_up'; clientMessageId: string }
  | { type: 'end_requested' }
  /** 끝내기를 보낸 뒤 답을 받기 전에 연결이 끊겼다. 서버는 끝냈을 가능성이 높다. */
  | { type: 'ended_unconfirmed' }
  | { type: 'notice_dismissed' };

export const initialConversationState: ConversationState = {
  phase: 'connecting',
  conversationId: null,
  recordDate: null,
  messages: [],
  awaitingReply: false,
  resources: null,
  ending: false,
  ended: null,
  diaryReady: false,
  notice: null,
  arrived: null,
};

function fromUtterance(utterance: WsUtterance): ChatMessage {
  return {
    key:
      utterance.client_message_id !== null
        ? `c:${utterance.client_message_id}`
        : `s:${utterance.seq}`,
    seq: utterance.seq,
    speaker: utterance.speaker,
    text: utterance.text,
    clientMessageId: utterance.client_message_id,
    delivery: 'sent',
  };
}

/** 서버가 아직 받지 못한, 이 화면에서 쓴 글 */
function unsent(state: ConversationState, stored: readonly ChatMessage[]): ChatMessage[] {
  const storedIds = new Set(stored.map((message) => message.clientMessageId));
  return state.messages.filter(
    (message) =>
      message.speaker === 'user' && message.seq === null && !storedIds.has(message.clientMessageId),
  );
}

/** 마지막 말이 사용자의 글이면 아직 답을 받지 못한 것이다. */
export function lastUnanswered(messages: readonly ChatMessage[]): ChatMessage | null {
  const last = messages.at(-1);
  return last !== undefined && last.speaker === 'user' ? last : null;
}

/** 서버가 준 순번대로 놓는다. 아직 순번이 없는 글(보내는 중)은 맨 뒤에 둔다. */
function ordered(messages: readonly ChatMessage[]): ChatMessage[] {
  const last = Number.MAX_SAFE_INTEGER;
  // 정렬은 자리가 같은 것들의 원래 순서를 지킨다. 순번이 없는 글끼리는 쓴 순서 그대로 남는다.
  return [...messages].sort((a, b) => (a.seq ?? last) - (b.seq ?? last));
}

function updateMessage(
  messages: readonly ChatMessage[],
  clientMessageId: string,
  change: Partial<ChatMessage>,
): ChatMessage[] {
  return messages.map((message) =>
    message.clientMessageId === clientMessageId ? { ...message, ...change } : message,
  );
}

function applyServerMessage(state: ConversationState, message: ServerMessage): ConversationState {
  switch (message.type) {
    case 'ready': {
      const stored = message.utterances.map(fromUtterance);
      const messages = [...stored, ...unsent(state, stored)];
      const continued =
        state.conversationId === null || state.conversationId === message.conversation_id;
      const unanswered = lastUnanswered(messages);
      return {
        ...state,
        phase: 'ready',
        conversationId: message.conversation_id,
        recordDate: message.record_date,
        messages,
        // 답을 받지 못한 글은 연결되자마자 같은 식별자로 다시 보낸다. 그동안은 기다리는 표시를 둔다.
        awaitingReply: unanswered !== null && unanswered.delivery !== 'failed',
        // 고정해 둔 자원은 서버가 그 대화에서 고정했다고 알려줄 때만 이어 간다. 새 대화에서는 비운다.
        resources: message.resources_pinned ? state.resources : null,
        notice: continued ? state.notice : 'new_conversation',
        arrived: null,
      };
    }

    case 'thinking':
      return {
        ...state,
        // 글을 보내는 사이에 서버가 먼저 건넨 말(무응답 확인)이 있으면 그 말이 앞선 순번을 갖는다.
        messages: ordered(
          updateMessage(state.messages, message.client_message_id, {
            seq: message.seq,
            delivery: 'sent',
          }),
        ),
        awaitingReply: true,
        notice:
          state.notice === 'slow_down' || state.notice === 'send_failed' ? null : state.notice,
      };

    case 'ai_text': {
      const incoming: ChatMessage = {
        key: `s:${message.seq}`,
        seq: message.seq,
        speaker: 'ai',
        text: message.text,
        clientMessageId: null,
        delivery: 'sent',
      };
      // 다시 연결하는 사이에 같은 말이 두 번 올 수 있다. 같은 순번이면 덮어쓴다.
      const exists = state.messages.some((item) => item.seq === message.seq);
      const messages = exists
        ? state.messages.map((item) => (item.seq === message.seq ? incoming : item))
        : ordered([...state.messages, incoming]);
      // 무응답 확인처럼 사용자의 글보다 앞선 순번의 말은 그 글의 답이 아니다. 계속 기다린다.
      const pending = [...state.messages].reverse().find((item) => item.speaker === 'user');
      const answersPending =
        pending === undefined || (pending.seq !== null && pending.seq < message.seq);
      return {
        ...state,
        messages,
        awaitingReply: answersPending ? false : state.awaitingReply,
        arrived: exists ? state.arrived : { seq: message.seq, text: message.text },
      };
    }

    case 'resources':
      // 빈 목록으로는 이미 고정해 둔 것을 지우지 않는다. 한 번 띄운 번호는 대화가 끝날 때까지 남아야 한다.
      return message.items.length === 0 ? state : { ...state, resources: message.items };

    case 'ended':
      return {
        ...state,
        phase: 'ended',
        awaitingReply: false,
        ending: false,
        ended: {
          reason: message.reason,
          recordDate: message.record_date,
          diaryExpected: message.diary_expected,
        },
        notice: null,
      };

    case 'diary_ready':
      return { ...state, diaryReady: true };

    case 'error':
      return applyServerError(state, message.code, message.client_message_id);
  }
}

function applyServerError(
  state: ConversationState,
  code: Extract<ServerMessage, { type: 'error' }>['code'],
  clientMessageId: string | undefined,
): ConversationState {
  switch (code) {
    case 'rate_limited':
      // 글은 보내는 중으로 둔다. 잠시 뒤에 같은 식별자로 다시 보낸다.
      return { ...state, notice: 'slow_down' };

    case 'message_too_large':
    case 'internal_error': {
      const notice = code === 'message_too_large' ? 'too_long' : 'send_failed';
      if (clientMessageId === undefined) {
        // 어느 글인지 모르면 답을 기다리던 글이 걸린 것으로 본다. 기다리는 표시가 영영 남지 않게 한다.
        const pending = lastUnanswered(state.messages);
        return {
          ...state,
          messages:
            pending?.clientMessageId != null
              ? updateMessage(state.messages, pending.clientMessageId, { delivery: 'failed' })
              : state.messages,
          awaitingReply: false,
          notice,
        };
      }
      return {
        ...state,
        messages: updateMessage(state.messages, clientMessageId, { delivery: 'failed' }),
        awaitingReply: false,
        notice,
      };
    }

    case 'conversation_ended':
      return {
        ...state,
        phase: 'ended',
        awaitingReply: false,
        ending: false,
        ended: { reason: 'elsewhere', recordDate: state.recordDate, diaryExpected: null },
        notice: null,
      };

    case 'invalid_message':
    case 'not_started':
    case 'already_started':
    case 'unsupported_mode':
      return { ...state, awaitingReply: false, notice: 'protocol' };
  }
}

export function conversationReducer(
  state: ConversationState,
  event: ConversationEvent,
): ConversationState {
  // 끝난 대화는 되살아나지 않는다. 끝난 뒤에 의미가 있는 것은 일기가 준비됐다는 소식뿐이다.
  if (state.phase === 'ended') {
    return event.type === 'server' && event.message.type === 'diary_ready'
      ? { ...state, diaryReady: true }
      : state;
  }

  switch (event.type) {
    case 'connecting':
      return {
        ...state,
        phase:
          state.conversationId === null && state.messages.length === 0
            ? 'connecting'
            : 'reconnecting',
      };

    case 'disconnected':
      return { ...state, phase: 'reconnecting' };

    case 'gave_up':
      return { ...state, phase: 'failed' };

    case 'server':
      return applyServerMessage(state, event.message);

    case 'submitted':
      return {
        ...state,
        messages: [
          ...state.messages,
          {
            key: `c:${event.clientMessageId}`,
            seq: null,
            speaker: 'user',
            text: event.text,
            clientMessageId: event.clientMessageId,
            delivery: 'sending',
          },
        ],
        awaitingReply: true,
        notice: null,
      };

    case 'retried':
      return {
        ...state,
        messages: updateMessage(state.messages, event.clientMessageId, { delivery: 'sending' }),
        awaitingReply: true,
        notice: null,
      };

    case 'send_gave_up':
      return {
        ...state,
        messages: updateMessage(state.messages, event.clientMessageId, { delivery: 'failed' }),
        awaitingReply: false,
        notice: 'send_failed',
      };

    case 'end_requested':
      return { ...state, ending: true };

    case 'ended_unconfirmed':
      return {
        ...state,
        phase: 'ended',
        awaitingReply: false,
        ending: false,
        ended: { reason: 'user', recordDate: state.recordDate, diaryExpected: null },
        notice: null,
      };

    case 'notice_dismissed':
      return { ...state, notice: null };
  }
}
