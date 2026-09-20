import type { WsClientMessage } from '@/api/types';
import {
  conversationReducer,
  initialConversationState,
  lastUnanswered,
  type ConversationEvent,
  type ConversationState,
} from '@/talk/conversationState';
import {
  CLOSE_CODE,
  encodeClientMessage,
  parseServerMessage,
  textLength,
  USER_TEXT_MAX_LENGTH,
  type ServerMessage,
} from '@/talk/messages';

/** 이 클라이언트가 소켓에서 쓰는 부분. 테스트는 같은 모양의 대역을 끼운다. */
export interface SocketLike {
  onopen: ((event: Event) => void) | null;
  onmessage: ((event: MessageEvent) => void) | null;
  onclose: ((event: CloseEvent) => void) | null;
  onerror: ((event: Event) => void) | null;
  send(data: string): void;
  close(code?: number, reason?: string): void;
}

export interface ConversationClientOptions {
  url: string;
  createSocket?: (url: string) => SocketLike;
  /** 글마다 붙이는 식별자(UUID)를 만든다. */
  newId?: () => string;
  /** 0 이상 1 미만의 난수. 다시 연결할 때까지의 기다림을 흩뜨리는 데 쓴다. */
  random?: () => number;
  isOnline?: () => boolean;
  /**
   * 연결이 열리기도 전에 닫혔을 때 부른다. 브라우저는 실패한 연결의 상태 코드를 알려주지 않는다.
   * 세션이 끝나서 거절된 것일 수 있으므로 부른 쪽이 로그인 상태를 다시 확인한다.
   */
  onHandshakeFailed?: () => void;
}

export const TIMING = {
  /** 연결을 열고 ready를 받을 때까지 기다리는 시간 */
  connectTimeoutMs: 10_000,
  /** 글을 보내고 thinking(받았다는 확인)을 기다리는 시간. 넘으면 같은 식별자로 다시 보낸다. */
  ackTimeoutMs: 8_000,
  /** thinking 뒤에 답을 기다리는 시간. 넘으면 연결이 조용히 죽은 것으로 보고 다시 잇는다. */
  replyTimeoutMs: 45_000,
  /** 끝내기를 보내고 ended를 기다리는 시간 */
  endTimeoutMs: 10_000,
  /** 너무 자주 보냈다는 답을 받았을 때 다시 보내기까지의 기다림 */
  rateLimitRetryMs: 3_000,
  reconnectBaseMs: 1_000,
  reconnectMaxMs: 30_000,
} as const;

/** 같은 글을 저절로 다시 보내는 횟수의 한도. 넘으면 사용자에게 맡긴다. */
export const MAX_SEND_ATTEMPTS = 3;
/** 이어서 실패한 연결 시도가 이만큼이면 그만두고 사용자에게 알린다. */
export const MAX_RECONNECT_ATTEMPTS = 6;

type Timer = ReturnType<typeof setTimeout>;

function uuidV4(): string {
  // randomUUID는 안전한 출처(HTTPS, localhost)에서만 있다. 같은 공유기의 폰으로 개발 서버를 열면 없다.
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6]! & 0x0f) | 0x40;
  bytes[8] = (bytes[8]! & 0x3f) | 0x80;
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

/**
 * 대화 채널 하나를 맡는다. 소켓을 열고, 끊기면 간격을 늘려 가며 다시 잇고, 답을 받지 못한 글을 같은 식별자로 다시 보낸다.
 * 화면은 subscribe와 getState로 상태만 읽는다. 리액트를 모른다.
 *
 * 받은 글과 보낸 글은 어디에도 기록하지 않는다. 오류를 만나도 내용을 콘솔에 찍지 않는다.
 */
export class ConversationClient {
  private state: ConversationState = initialConversationState;
  private readonly listeners = new Set<() => void>();

  private readonly url: string;
  private readonly createSocket: (url: string) => SocketLike;
  private readonly newId: () => string;
  private readonly random: () => number;
  private readonly isOnline: () => boolean;
  private readonly onHandshakeFailed: (() => void) | undefined;

  private socket: SocketLike | null = null;
  /** 지금 소켓이 열린 적이 있는지. 열리기도 전에 닫혔다면 서버가 연결 자체를 거절한 것이다. */
  private opened = false;
  /** 화면을 떠나 있는 동안에는 참이다. 다시 연결하지 않고 타이머도 돌리지 않는다. */
  private stopped = true;
  /** 이어서 실패한 연결 시도의 수. ready를 받으면 0으로 돌아간다. */
  private failures = 0;
  private endWanted = false;
  private endSent = false;
  private readonly sendAttempts = new Map<string, number>();

  private connectTimer: Timer | null = null;
  private reconnectTimer: Timer | null = null;
  private ackTimer: Timer | null = null;
  private replyTimer: Timer | null = null;
  private endTimer: Timer | null = null;
  private rateLimitTimer: Timer | null = null;

  constructor(options: ConversationClientOptions) {
    this.url = options.url;
    this.createSocket = options.createSocket ?? ((url) => new WebSocket(url));
    this.newId = options.newId ?? uuidV4;
    this.random = options.random ?? Math.random;
    this.isOnline = options.isOnline ?? (() => navigator.onLine);
    this.onHandshakeFailed = options.onHandshakeFailed;
  }

  // ---- 화면이 쓰는 것 ---------------------------------------------------------------

  readonly subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  readonly getState = (): ConversationState => this.state;

  /** 연결을 시작한다. stop으로 멈춘 뒤에 다시 불러도 된다. */
  start(): void {
    this.stopped = false;
    if (this.socket !== null || this.state.phase === 'ended') return;
    this.failures = 0;
    this.open();
  }

  /**
   * 글을 보낸다. 보낼 수 없는 글이면 false를 돌려준다.
   * 답을 기다리는 동안에는 다음 글을 받지 않는다. 글마다 관문과 답이 한 바퀴씩 돌기 때문에 겹쳐 보내면 답의 짝이 어긋난다.
   */
  send(text: string): boolean {
    const trimmed = text.trim();
    if (trimmed === '' || textLength(trimmed) > USER_TEXT_MAX_LENGTH) return false;
    if (this.state.phase !== 'ready' || this.state.awaitingReply || this.state.ending) return false;

    const clientMessageId = this.newId();
    this.dispatch({ type: 'submitted', clientMessageId, text: trimmed });
    this.transmit(clientMessageId, trimmed);
    return true;
  }

  /** 보내지 못한 글을 같은 식별자로 다시 보낸다. 몇 번을 보내도 서버에는 한 번만 저장된다. */
  retry(clientMessageId: string): void {
    const message = this.state.messages.find((item) => item.clientMessageId === clientMessageId);
    if (message === undefined || message.delivery !== 'failed') return;
    if (this.state.phase !== 'ready' || this.state.awaitingReply) return;

    this.sendAttempts.delete(clientMessageId);
    this.dispatch({ type: 'retried', clientMessageId });
    this.transmit(clientMessageId, message.text);
  }

  /** 끝내기. 연결이 끊겨 있으면 다시 이어진 뒤에 보낸다. */
  end(): void {
    if (this.state.phase === 'ended' || this.endWanted) return;
    this.endWanted = true;
    this.dispatch({ type: 'end_requested' });
    if (this.state.phase === 'ready') this.transmitEnd();
    else this.wake();
  }

  /** 기다리지 않고 지금 다시 연결해 본다. 연결이 돌아왔을 때, 앱으로 돌아왔을 때, 사용자가 다시 시도를 눌렀을 때 부른다. */
  wake(): void {
    if (this.stopped || this.state.phase === 'ended' || this.socket !== null) return;
    this.clear('reconnectTimer');
    this.failures = 0;
    this.open();
  }

  dismissNotice(): void {
    this.dispatch({ type: 'notice_dismissed' });
  }

  /** 화면을 떠날 때 부른다. 대화는 서버에 열린 채로 남고, 돌아와서 start를 부르면 이어진다. */
  stop(): void {
    this.stopped = true;
    this.clearAllTimers();
    this.closeSocket();
  }

  // ---- 연결 -------------------------------------------------------------------------

  private open(): void {
    this.dispatch({ type: 'connecting' });
    this.opened = false;

    let socket: SocketLike;
    try {
      socket = this.createSocket(this.url);
    } catch {
      // 주소가 틀렸거나 브라우저가 연결을 막았다. 끊긴 것과 똑같이 다룬다.
      this.handleClose();
      return;
    }
    this.socket = socket;

    socket.onopen = () => {
      if (this.socket !== socket) return;
      this.opened = true;
      this.sendFrame({ type: 'start', mode: 'chat' });
    };
    socket.onmessage = (event) => {
      if (this.socket !== socket) return;
      const message = parseServerMessage(event.data);
      if (message !== null) this.handleServerMessage(message);
    };
    socket.onclose = (event) => {
      if (this.socket !== socket) return;
      this.socket = null;
      this.handleClose(event.code);
    };
    // 오류 뒤에는 언제나 close가 따라온다. 뒷일은 거기서 한다.
    socket.onerror = null;

    this.connectTimer = setTimeout(() => {
      this.connectTimer = null;
      // 열리지 않거나, 열렸는데 ready가 오지 않는다. 닫고 다시 잇는다.
      this.dropSocket();
    }, TIMING.connectTimeoutMs);
  }

  /** code는 서버가 닫으면서 붙인 코드다. 연결이 그냥 끊겼거나 이쪽에서 버린 연결에는 없다. */
  private handleClose(code?: number): void {
    this.clear('connectTimer');
    this.clear('ackTimer');
    this.clear('replyTimer');
    this.clear('endTimer');
    this.clear('rateLimitTimer');
    if (this.stopped || this.state.phase === 'ended') return;

    if (this.endSent) {
      // 끝내기는 나갔는데 답을 듣지 못했다. 다시 연결하면 서버가 새 대화를 열어 버린다.
      // 끝난 것으로 보고, 일기가 준비됐는지는 화면이 직접 확인하게 둔다.
      this.dispatch({ type: 'ended_unconfirmed' });
      return;
    }

    if (code === CLOSE_CODE.takenOver) {
      // 다른 화면이 대화를 이어받았다. 여기서 다시 이으면 그 화면에서 다시 빼앗아 오고,
      // 두 화면이 끝없이 대화를 주고받는다. 이어가려면 사용자가 고르게 한다.
      this.dispatch({ type: 'taken_over' });
      return;
    }
    if (code === CLOSE_CODE.gone) {
      // 계정이 사라졌다. 다시 이어도 같은 까닭으로 닫힌다. 로그인 상태를 확인하면 경로 보호가 데리고 나간다.
      this.onHandshakeFailed?.();
      this.dispatch({ type: 'gave_up' });
      return;
    }

    // 이어지는 실패마다 묻지 않는다. 첫 실패에 한 번이면 세션이 끝났는지 알 수 있다.
    if (!this.opened && this.failures === 0) this.onHandshakeFailed?.();

    this.failures += 1;
    if (this.failures >= MAX_RECONNECT_ATTEMPTS) {
      this.dispatch({ type: 'gave_up' });
      return;
    }
    this.dispatch({ type: 'disconnected' });
    // 연결이 끊긴 기기로는 두드려 봐야 실패만 쌓인다. 연결이 돌아오면 화면이 wake를 부른다.
    if (!this.isOnline()) return;

    const ceiling = Math.min(
      TIMING.reconnectMaxMs,
      TIMING.reconnectBaseMs * 2 ** (this.failures - 1),
    );
    // 서버가 다시 뜬 순간에 모두가 한꺼번에 몰리지 않게 기다림을 절반에서 전부 사이로 흩뜨린다.
    const delay = Math.round(ceiling / 2 + this.random() * (ceiling / 2));
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (!this.stopped && this.socket === null) this.open();
    }, delay);
  }

  /** 지금 소켓을 버리고 끊긴 것으로 다룬다. 죽은 연결은 close 이벤트가 한참 뒤에야 오기도 해서 기다리지 않는다. */
  private dropSocket(): void {
    this.closeSocket();
    this.handleClose();
  }

  private closeSocket(): void {
    const socket = this.socket;
    if (socket === null) return;
    this.socket = null;
    socket.onopen = socket.onmessage = socket.onclose = socket.onerror = null;
    try {
      socket.close(1000);
    } catch {
      // 이미 닫힌 소켓이다.
    }
  }

  // ---- 받기 -------------------------------------------------------------------------

  private handleServerMessage(message: ServerMessage): void {
    this.dispatch({ type: 'server', message });

    switch (message.type) {
      case 'ready':
        this.clear('connectTimer');
        this.failures = 0;
        if (this.endWanted) this.transmitEnd();
        else this.resendUnanswered();
        break;

      case 'thinking':
        this.clear('ackTimer');
        this.armReplyTimer();
        break;

      case 'ai_text':
        if (!this.state.awaitingReply) this.clear('replyTimer');
        break;

      case 'ended':
        this.clearAllTimers();
        // 일기가 준비됐다는 소식을 들으려고 연결은 열어 둔다. 기다릴 것이 없으면 닫는다.
        if (!message.diary_expected) this.closeSocket();
        break;

      case 'diary_ready':
        this.closeSocket();
        break;

      case 'error':
        this.handleServerError(message);
        break;

      case 'resources':
        break;
    }
  }

  private handleServerError(message: Extract<ServerMessage, { type: 'error' }>): void {
    switch (message.code) {
      case 'rate_limited': {
        this.clear('ackTimer');
        const pending = lastUnanswered(this.state.messages);
        if (pending?.clientMessageId == null || pending.delivery !== 'sending') return;
        const { clientMessageId, text } = pending;
        this.rateLimitTimer = setTimeout(() => {
          this.rateLimitTimer = null;
          this.transmit(clientMessageId, text);
        }, TIMING.rateLimitRetryMs);
        break;
      }
      case 'conversation_ended':
        this.clearAllTimers();
        this.closeSocket();
        break;
      default:
        this.clear('ackTimer');
        this.clear('replyTimer');
        break;
    }
  }

  // ---- 보내기 -----------------------------------------------------------------------

  private resendUnanswered(): void {
    const pending = lastUnanswered(this.state.messages);
    if (pending?.clientMessageId == null || pending.delivery === 'failed') return;
    this.transmit(pending.clientMessageId, pending.text);
  }

  private transmit(clientMessageId: string, text: string): void {
    const attempts = (this.sendAttempts.get(clientMessageId) ?? 0) + 1;
    if (attempts > MAX_SEND_ATTEMPTS) {
      this.clear('ackTimer');
      this.dispatch({ type: 'send_gave_up', clientMessageId });
      return;
    }
    // 연결이 없으면 보내지 않는다. 다시 이어져 ready를 받으면 거기서 보낸다.
    if (this.socket === null || this.state.phase !== 'ready') return;

    this.sendAttempts.set(clientMessageId, attempts);
    if (!this.sendFrame({ type: 'user_text', client_message_id: clientMessageId, text })) return;

    this.clear('ackTimer');
    this.ackTimer = setTimeout(() => {
      this.ackTimer = null;
      // 받았다는 확인이 없다. 글이 가다가 사라졌거나 연결이 조용히 죽은 것이다. 어느 쪽인지 알 수 없으므로
      // 연결부터 새로 잇는다. ready를 받으면 같은 식별자로 다시 보낸다.
      this.dropSocket();
    }, TIMING.ackTimeoutMs);
  }

  private armReplyTimer(): void {
    this.clear('replyTimer');
    this.replyTimer = setTimeout(() => {
      this.replyTimer = null;
      // 받았다는 확인은 왔는데 답이 없다. 연결이 조용히 죽었을 수 있다. 다시 이으면 지난 발화와 함께 이어진다.
      this.dropSocket();
    }, TIMING.replyTimeoutMs);
  }

  private transmitEnd(): void {
    if (!this.sendFrame({ type: 'end' })) return;
    this.endSent = true;
    this.clear('endTimer');
    this.endTimer = setTimeout(() => {
      this.endTimer = null;
      this.dropSocket();
    }, TIMING.endTimeoutMs);
  }

  /** 프레임 하나를 보낸다. 보내지 못했으면 연결을 버리고 false를 돌려준다. */
  private sendFrame(message: WsClientMessage): boolean {
    if (this.socket === null) return false;
    try {
      this.socket.send(encodeClientMessage(message));
      return true;
    } catch {
      // 소켓이 보낼 수 없는 상태다. 오류의 내용은 남기지 않는다.
      this.dropSocket();
      return false;
    }
  }

  // ---- 상태와 타이머 ----------------------------------------------------------------

  private dispatch(event: ConversationEvent): void {
    const next = conversationReducer(this.state, event);
    if (next === this.state) return;
    this.state = next;
    for (const listener of this.listeners) listener();
  }

  private clear(
    name:
      'connectTimer' | 'reconnectTimer' | 'ackTimer' | 'replyTimer' | 'endTimer' | 'rateLimitTimer',
  ): void {
    const timer = this[name];
    if (timer !== null) clearTimeout(timer);
    this[name] = null;
  }

  private clearAllTimers(): void {
    this.clear('connectTimer');
    this.clear('reconnectTimer');
    this.clear('ackTimer');
    this.clear('replyTimer');
    this.clear('endTimer');
    this.clear('rateLimitTimer');
  }
}
