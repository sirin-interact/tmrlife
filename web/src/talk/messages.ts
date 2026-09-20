import type {
  Resource,
  WsClientMessage,
  WsEndReason,
  WsErrorCode,
  WsOrigin,
  WsServerMessage,
  WsSpeaker,
  WsUtterance,
} from '@/api/types';

/** 대화 채널의 경로. 화면과 같은 출처로 연결해야 세션 쿠키가 실리고 서버의 출처 확인을 통과한다. */
export const CONVERSATION_PATH = '/ws/v1/conversation';

/** 글 하나의 최대 길이(글자 수). 명세의 값과 같아야 한다. 넘으면 서버가 메시지 전체를 거절한다. */
export const USER_TEXT_MAX_LENGTH = 2000;

export function conversationUrl(location: Pick<Location, 'protocol' | 'host'>): string {
  const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${scheme}//${location.host}${CONVERSATION_PATH}`;
}

// 값을 빠뜨리면 컴파일이 멈추도록 Record로 적는다. 명세에 값이 늘면 여기서 먼저 드러난다.
const SPEAKERS: Record<WsSpeaker, true> = { user: true, ai: true };
const ORIGINS: Record<WsOrigin, true> = { user: true, model: true, fixed: true, template: true };
const END_REASONS: Record<WsEndReason, true> = { user: true, idle: true };
const ERROR_CODES: Record<WsErrorCode, true> = {
  invalid_message: true,
  message_too_large: true,
  rate_limited: true,
  not_started: true,
  already_started: true,
  unsupported_mode: true,
  conversation_ended: true,
  internal_error: true,
};

/** 대화가 끝난 까닭. 이 앱이 모르는 값을 서버가 보내면 'other'로 읽는다. */
export type EndReason = WsEndReason | 'other';

/** 서버가 보낸 ended. 끝난 까닭만 이 앱이 아는 값으로 좁혀 둔다. */
export type ServerMessage =
  | Exclude<WsServerMessage, { type: 'ended' }>
  | (Omit<Extract<WsServerMessage, { type: 'ended' }>, 'reason'> & { reason: EndReason });

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isSeq(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}

function isKnown<T extends string>(value: unknown, known: Record<T, true>): value is T {
  return typeof value === 'string' && Object.hasOwn(known, value);
}

function parseResource(value: unknown): Resource | null {
  if (!isRecord(value)) return null;
  const { id, name, phone, description } = value;
  if (typeof id !== 'string' || typeof name !== 'string' || typeof phone !== 'string') return null;
  return { id, name, phone, description: typeof description === 'string' ? description : '' };
}

function parseResources(value: unknown): Resource[] | null {
  if (!Array.isArray(value)) return null;
  // 모양이 틀린 항목 하나 때문에 나머지 번호까지 버리지 않는다.
  return value.map(parseResource).filter((item) => item !== null);
}

function parseUtterance(value: unknown): WsUtterance | null {
  if (!isRecord(value)) return null;
  const { seq, speaker, origin, text, client_message_id: clientMessageId } = value;
  if (!isSeq(seq) || !isKnown(speaker, SPEAKERS) || typeof text !== 'string') return null;
  return {
    seq,
    speaker,
    // 화면은 말을 만든 쪽을 가려 쓰지 않는다. 모르는 값이 와도 발화를 버리지 않는다.
    origin: isKnown(origin, ORIGINS) ? origin : speaker === 'user' ? 'user' : 'model',
    text,
    client_message_id: typeof clientMessageId === 'string' ? clientMessageId : null,
    created_at: typeof value.created_at === 'string' ? value.created_at : '',
  };
}

/**
 * 서버가 보낸 프레임 하나를 읽는다. 읽을 수 없거나 이 앱이 모르는 종류면 null이다.
 * 서버가 이 앱보다 새 판일 수 있으므로(설치된 앱은 옛 코드를 오래 들고 있다) 모르는 것이 와도 던지지 않는다.
 */
export function parseServerMessage(data: unknown): ServerMessage | null {
  if (typeof data !== 'string') return null;

  let value: unknown;
  try {
    value = JSON.parse(data);
  } catch {
    return null;
  }
  if (!isRecord(value)) return null;

  switch (value.type) {
    case 'ready': {
      const { conversation_id: conversationId, record_date: recordDate } = value;
      if (typeof conversationId !== 'string' || typeof recordDate !== 'string') return null;
      const utterances = Array.isArray(value.utterances)
        ? value.utterances.map(parseUtterance).filter((item) => item !== null)
        : [];
      return {
        type: 'ready',
        conversation_id: conversationId,
        record_date: recordDate,
        resumed: value.resumed === true,
        utterances,
        resources_pinned: value.resources_pinned === true,
      };
    }
    case 'thinking': {
      const { client_message_id: clientMessageId, seq } = value;
      if (typeof clientMessageId !== 'string' || !isSeq(seq)) return null;
      return { type: 'thinking', client_message_id: clientMessageId, seq };
    }
    case 'ai_text': {
      const { seq, text, origin } = value;
      if (!isSeq(seq) || typeof text !== 'string') return null;
      return { type: 'ai_text', seq, text, origin: isKnown(origin, ORIGINS) ? origin : 'model' };
    }
    case 'resources': {
      const items = parseResources(value.items);
      return items === null ? null : { type: 'resources', items };
    }
    case 'ended': {
      const { reason, record_date: recordDate } = value;
      if (typeof recordDate !== 'string') return null;
      return {
        type: 'ended',
        reason: isKnown(reason, END_REASONS) ? reason : 'other',
        record_date: recordDate,
        diary_expected: value.diary_expected === true,
      };
    }
    case 'diary_ready': {
      const { record_date: recordDate } = value;
      if (typeof recordDate !== 'string') return null;
      return { type: 'diary_ready', record_date: recordDate };
    }
    case 'error': {
      const { code, client_message_id: clientMessageId } = value;
      return {
        type: 'error',
        // 모르는 코드는 서버 쪽 문제로 읽는다. 글이 걸려 있으면 다시 보낼 수 있게 된다.
        code: isKnown(code, ERROR_CODES) ? code : 'internal_error',
        ...(typeof clientMessageId === 'string' && { client_message_id: clientMessageId }),
      };
    }
    default:
      return null;
  }
}

export function encodeClientMessage(message: WsClientMessage): string {
  return JSON.stringify(message);
}

/** 명세가 세는 방식(유니코드 글자 수)으로 길이를 센다. 이모지는 자바스크립트의 length로는 둘로 세어진다. */
export function textLength(text: string): number {
  return Array.from(text).length;
}
