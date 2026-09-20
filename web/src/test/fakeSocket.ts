import { vi } from 'vitest';

import type { Resource, WsServerMessage, WsUtterance } from '@/api/types';
import type { SocketLike } from '@/talk/conversationClient';

/** 테스트가 서버 노릇을 할 수 있는 소켓 대역. 보낸 프레임을 모아 두고, 서버의 메시지와 끊김을 흉내 낸다. */
export class FakeSocket implements SocketLike {
  onopen: SocketLike['onopen'] = null;
  onmessage: SocketLike['onmessage'] = null;
  onclose: SocketLike['onclose'] = null;
  onerror: SocketLike['onerror'] = null;

  /** 클라이언트가 보낸 프레임을 JSON으로 읽은 것 */
  readonly sent: Array<Record<string, unknown>> = [];
  readonly url: string;
  closedByClient = false;

  constructor(url: string) {
    this.url = url;
  }

  send(data: string): void {
    this.sent.push(JSON.parse(data) as Record<string, unknown>);
  }

  close(): void {
    this.closedByClient = true;
  }

  // ---- 서버 노릇 --------------------------------------------------------------------

  /** 연결이 열렸다. */
  open(): void {
    this.onopen?.(new Event('open'));
  }

  receive(message: WsServerMessage | Record<string, unknown>): void {
    this.receiveRaw(JSON.stringify(message));
  }

  receiveRaw(data: unknown): void {
    this.onmessage?.(new MessageEvent('message', { data }));
  }

  /**
   * 연결이 끊겼다(서버가 닫았거나 네트워크가 끊겼다).
   * code를 주면 서버가 그 코드로 닫은 것이다. 기본값은 인사 없이 끊겼을 때 브라우저가 주는 코드다.
   */
  drop(code = 1006): void {
    this.onclose?.(new CloseEvent('close', { code }));
  }

  sentOfType(type: string): Array<Record<string, unknown>> {
    return this.sent.filter((frame) => frame.type === type);
  }
}

export interface FakeSockets {
  all: FakeSocket[];
  create: (url: string) => FakeSocket;
  /** 가장 최근에 만든 소켓. 없으면 테스트가 실패한다. */
  latest: () => FakeSocket;
}

export function fakeSockets(): FakeSockets {
  const all: FakeSocket[] = [];
  return {
    all,
    create: (url) => {
      const socket = new FakeSocket(url);
      all.push(socket);
      return socket;
    },
    latest: () => {
      const socket = all.at(-1);
      if (!socket) throw new Error('아직 소켓을 만들지 않았다');
      return socket;
    },
  };
}

/** 전역 WebSocket을 대역으로 바꿔 끼운다. 화면 테스트에서 쓴다. */
export function installFakeWebSocket(): FakeSockets {
  const sockets = fakeSockets();
  vi.stubGlobal(
    'WebSocket',
    // new로 부르므로 화살표 함수가 아니어야 한다. 객체를 돌려주면 그것이 new의 결과가 된다.
    function WebSocket(url: string) {
      return sockets.create(url);
    },
  );
  return sockets;
}

// ---- 서버 메시지를 짧게 만드는 도우미 ---------------------------------------------

export const CONVERSATION_ID = '0199a1b2-0000-7000-8000-00000000c0de';
export const RECORD_DATE = '2026-09-20';

export function ready(
  overrides: Partial<Extract<WsServerMessage, { type: 'ready' }>> = {},
): WsServerMessage {
  return {
    type: 'ready',
    conversation_id: CONVERSATION_ID,
    record_date: RECORD_DATE,
    resumed: false,
    utterances: [],
    resources_pinned: false,
    ...overrides,
  };
}

export function utterance(
  seq: number,
  speaker: 'user' | 'ai',
  text: string,
  clientMessageId: string | null = null,
): WsUtterance {
  return {
    seq,
    speaker,
    origin: speaker === 'user' ? 'user' : 'model',
    text,
    client_message_id: clientMessageId,
    created_at: '2026-09-20T11:00:00.000Z',
  };
}

export const aiText = (seq: number, text: string): WsServerMessage => ({
  type: 'ai_text',
  seq,
  text,
  origin: 'model',
});

export const thinking = (clientMessageId: string, seq: number): WsServerMessage => ({
  type: 'thinking',
  client_message_id: clientMessageId,
  seq,
});

export const testResources: Resource[] = [
  {
    id: 'suicide_prevention_109',
    name: '자살예방상담전화',
    phone: '109',
    description: '24시간, 지금 바로 이야기를 들어줄 사람과 연결돼요.',
  },
  {
    id: 'mental_health_crisis_1577_0199',
    name: '정신건강위기상담',
    phone: '1577-0199',
    description: '24시간, 마음이 위태로울 때 전화하면 가까운 지역의 도움과 이어 줘요.',
  },
  {
    id: 'emergency_119',
    name: '긴급 상황',
    phone: '119',
    description: '몸이 위험하거나 한시가 급할 때 바로 걸어요.',
  },
];
