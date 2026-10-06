import type { ConversationMode, Resource, WsUtterance } from '@/api/types';
import type { EndReason, ServerMessage } from '@/talk/messages';

/**
 * 대화 화면의 상태. 소켓도 타이머도 마이크도 모르는 순수한 계산이다.
 * 무슨 일이 일어났는지(사건)를 받아 다음 상태를 돌려준다. 소켓을 열고 다시 보내고 마이크를 켜는 일은 ConversationClient가 한다.
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
  /** 같은 계정의 다른 화면이 대화를 이어받았다. 여기서 이어가려면 사용자가 다시 연결해야 한다. */
  | 'taken_over'
  /** 대화가 끝났다. */
  | 'ended';

export type Delivery = 'sending' | 'sent' | 'failed';

/** 이 기기의 마이크 상태. 서버가 아는 대화 방식(mode)과는 다른 것이다. */
export type MicState =
  | 'off'
  /** 권한을 묻고 오디오 장치를 여는 중 */
  | 'starting'
  | 'on'
  /** 브라우저가 마이크 권한을 거절했다. 설정에서 허용해야 다시 켤 수 있다. */
  | 'denied'
  /** 마이크가 없거나 열지 못했다. */
  | 'failed';

/** 마이크를 켜지 못한 까닭. 안내 문구가 달라진다. */
export type MicFailure = 'denied' | 'missing' | 'insecure' | 'failed';

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
  | 'protocol'
  /** 서버가 음성을 열 수 없거나 더 이어갈 수 없어 글로 내려왔다 */
  | 'voice_unavailable'
  /** 브라우저가 마이크 권한을 거절했다 */
  | 'mic_denied'
  /** 쓸 수 있는 마이크가 없다 */
  | 'mic_missing'
  /** 안전하지 않은 주소(http의 다른 기기)라 브라우저가 마이크를 내주지 않는다 */
  | 'mic_insecure'
  /** 마이크는 열렸는데 소리가 전혀 들어오지 않는다(소리 없는 가상 마이크, 꺼진 입력 장치) */
  | 'mic_silent'
  /** 마이크를 열지 못했다(장치 오류, 오디오 모듈을 받지 못함) */
  | 'mic_failed';

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
  /** 서버가 알고 있는 실제 대화 방식. 'voice'일 때만 소리를 올려 보낸다. */
  mode: ConversationMode;
  /**
   * 서버가 지금 사용자의 말을 듣고 있는지. 한 번에 한 마디만 듣는다. 끝점이 오면 서버가 스스로 멈추고,
   * 사용자가 구슬을 눌러 다시 청할 때까지 소리를 보내지 않는다. 혼잣말과 주변 말이 턴이 되지 않게 하려는 것이다.
   */
  listening: boolean;
  /** 이 서버가 음성을 쓸 수 있는지. 거짓이면 음성으로 바꾸는 버튼을 두지 않는다. */
  voiceAvailable: boolean;
  /** 지금 말하고 있는 한 마디의 중간 자막. 끝점이 오거나 턴이 돌기 시작하면 비운다. */
  partial: string | null;
  /** 어느 말의 소리를 들려주고 있는지. 재생이 다 끝나야 비운다. */
  speaking: { seq: number } | null;
  /** 소리가 붙은 마지막 말의 순번. 화면이 그 말을 글로만 온 것처럼 시간으로 흉내 내지 않게 한다. */
  voicedSeq: number | null;
  mic: MicState;
}

export type ConversationEvent =
  | { type: 'connecting' }
  | { type: 'disconnected' }
  | { type: 'gave_up' }
  /** 다른 화면이 대화를 이어받아 이 연결이 물러났다 */
  | { type: 'taken_over' }
  | { type: 'server'; message: ServerMessage }
  | { type: 'submitted'; clientMessageId: string; text: string }
  | { type: 'retried'; clientMessageId: string }
  | { type: 'send_gave_up'; clientMessageId: string }
  | { type: 'end_requested' }
  /** 끝내기를 보낸 뒤 답을 받기 전에 연결이 끊겼다. 서버는 끝냈을 가능성이 높다. */
  | { type: 'ended_unconfirmed' }
  | { type: 'notice_dismissed' }
  /** 마이크를 켜거나 끄는 중의 상태 */
  | { type: 'mic'; mic: 'off' | 'starting' | 'on' }
  | { type: 'mic_failed'; reason: MicFailure }
  /** 마이크가 열린 채 한동안 아무 소리도 오지 않았다 */
  | { type: 'mic_silent' }
  /** 소리가 다시 들어온다. 무음 알림을 거둔다. */
  | { type: 'mic_sound' }
  /** 서버가 이쪽이 바라지 않았는데 글로 내려 보냈다(음성이 죽었다). */
  | { type: 'voice_dropped' }
  /** 받아 둔 소리를 끝까지 들려줬거나, 끼어들어 버렸다. */
  | { type: 'playback_finished'; seq: number };

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
  mode: 'chat',
  listening: false,
  voiceAvailable: false,
  partial: null,
  speaking: null,
  voicedSeq: null,
  mic: 'off',
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

function applyTranscript(
  state: ConversationState,
  message: Extract<ServerMessage, { type: 'transcript' }>,
): ConversationState {
  if (!message.final) {
    return { ...state, partial: message.text === '' ? null : message.text };
  }

  const clientMessageId = message.client_message_id;
  if (clientMessageId === undefined) {
    // 식별자가 없으면 화면에 올릴 수 없지만, 그 말로 턴이 도는 것은 같다. 답을 기다리는 표시만 둔다.
    return { ...state, partial: null, awaitingReply: true };
  }
  const exists = state.messages.some((item) => item.clientMessageId === clientMessageId);
  const messages = exists
    ? updateMessage(state.messages, clientMessageId, { text: message.text })
    : [
        ...state.messages,
        {
          key: `c:${clientMessageId}`,
          seq: null,
          speaker: 'user' as const,
          text: message.text,
          clientMessageId,
          // 서버가 알아들은 말이라 이미 서버에 있다. 보내는 중이 아니다.
          delivery: 'sent' as const,
        },
      ];
  return { ...state, messages, partial: null, awaitingReply: true, notice: null };
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
        mode: message.mode,
        voiceAvailable: message.voice_available,
        partial: null,
        speaking: null,
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
        // 턴이 돌기 시작했다. 중간 자막은 그 말의 확정본이 목록에 올랐으니 더 보일 까닭이 없다.
        partial: null,
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
        partial: null,
        speaking: null,
        mic: 'off',
      };

    case 'diary_ready':
      return { ...state, diaryReady: true };

    case 'mode':
      return message.mode === 'voice'
        ? { ...state, mode: 'voice' }
        : // 글로 내려왔다. 소리는 더 오지 않고, 마이크는 ConversationClient가 거둔다.
          { ...state, mode: 'chat', listening: false, partial: null, speaking: null, mic: 'off' };

    case 'listening':
      // 듣기를 멈추면 지금 말하던 한 마디의 중간 자막도 끝난 것이다.
      return {
        ...state,
        listening: message.active,
        partial: message.active ? state.partial : null,
      };

    case 'transcript':
      return applyTranscript(state, message);

    case 'audio_start':
      return { ...state, speaking: { seq: message.seq }, voicedSeq: message.seq };

    case 'audio_end':
      // 끝까지 보냈으면 받아 둔 소리가 다 나갈 때까지 들려준다. 그 끝은 playback_finished가 알린다.
      if (message.reason === 'done') return state;
      return state.speaking?.seq === message.seq ? { ...state, speaking: null } : state;

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
        partial: null,
        speaking: null,
        mic: 'off',
      };

    case 'voice_unavailable':
      // 대화는 글로 이어진다. 답을 기다리던 글이 있으면 그대로 기다린다.
      return {
        ...state,
        mode: 'chat',
        mic: 'off',
        partial: null,
        speaking: null,
        notice: 'voice_unavailable',
      };

    case 'invalid_message':
    case 'not_started':
    case 'already_started':
    case 'unsupported_mode':
      return { ...state, awaitingReply: false, notice: 'protocol' };
  }
}

function applyMicFailure(state: ConversationState, reason: MicFailure): ConversationState {
  switch (reason) {
    case 'denied':
      return { ...state, mic: 'denied', notice: 'mic_denied' };
    case 'missing':
      return { ...state, mic: 'failed', notice: 'mic_missing' };
    case 'insecure':
      return { ...state, mic: 'failed', notice: 'mic_insecure' };
    case 'failed':
      return { ...state, mic: 'failed', notice: 'mic_failed' };
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
        // 소리는 연결과 함께 끊긴다. 새 연결에서는 audio_start부터 다시 온다. 듣기도 새 연결에서 다시 청한다.
        partial: null,
        listening: false,
        speaking: null,
      };

    case 'disconnected':
      return { ...state, phase: 'reconnecting', partial: null, speaking: null, listening: false };

    case 'gave_up':
      return { ...state, phase: 'failed', partial: null, speaking: null };

    case 'taken_over':
      // 이 연결로는 답이 오지 않는다. 기다리는 표시를 남겨 두지 않는다. 마이크는 이어받은 화면의 몫이다.
      return {
        ...state,
        phase: 'taken_over',
        awaitingReply: false,
        partial: null,
        speaking: null,
        mic: 'off',
      };

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
        partial: null,
        speaking: null,
        mic: 'off',
      };

    case 'notice_dismissed':
      return { ...state, notice: null };

    case 'mic':
      // 마이크를 끄면 중간 자막도 의미를 잃는다. 켜지는 중에는 지난 거절 알림을 거둔다.
      return {
        ...state,
        mic: event.mic,
        listening: event.mic === 'off' ? false : state.listening,
        partial: event.mic === 'off' ? null : state.partial,
        notice:
          (event.mic === 'starting' &&
            (state.notice === 'mic_denied' ||
              state.notice === 'mic_missing' ||
              state.notice === 'mic_insecure' ||
              state.notice === 'mic_failed')) ||
          state.notice === 'mic_silent'
            ? null
            : state.notice,
      };

    case 'mic_failed':
      return applyMicFailure(state, event.reason);

    case 'mic_silent':
      return state.mic === 'on' ? { ...state, notice: 'mic_silent' } : state;

    case 'mic_sound':
      return state.notice === 'mic_silent' ? { ...state, notice: null } : state;

    case 'voice_dropped':
      return {
        ...state,
        mode: 'chat',
        listening: false,
        mic: 'off',
        partial: null,
        speaking: null,
        notice: 'voice_unavailable',
      };

    case 'playback_finished':
      return state.speaking?.seq === event.seq ? { ...state, speaking: null } : state;
  }
}
