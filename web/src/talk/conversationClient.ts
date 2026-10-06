import type { WsClientMessage } from '@/api/types';
import { MicCapture, micFailureOf, type CaptureLike } from '@/talk/audio/capture';
import { AudioPlayer, type PlayerLike } from '@/talk/audio/player';
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
import { rememberPreferredMode } from '@/talk/modePreference';

/** 이 클라이언트가 소켓에서 쓰는 부분. 테스트는 같은 모양의 대역을 끼운다. */
export interface SocketLike {
  /** 바이너리 프레임(소리)을 Blob이 아니라 ArrayBuffer로 받으려고 'arraybuffer'로 둔다. */
  binaryType: BinaryType;
  onopen: ((event: Event) => void) | null;
  onmessage: ((event: MessageEvent) => void) | null;
  onclose: ((event: CloseEvent) => void) | null;
  onerror: ((event: Event) => void) | null;
  send(data: string | ArrayBuffer): void;
  close(code?: number, reason?: string): void;
}

export interface ConversationClientOptions {
  url: string;
  createSocket?: (url: string) => SocketLike;
  /** 마이크. 테스트는 Web Audio 없이 조각을 흘려 넣는 대역을 끼운다. */
  createCapture?: () => CaptureLike;
  /** 재생기. 테스트는 받은 조각을 모아 두는 대역을 끼운다. */
  createPlayer?: () => PlayerLike;
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

/**
 * 이보다 작은 소리 크기는 아무것도 들어오지 않는 것으로 본다. 조용한 방의 마이크도 이보다는 크다.
 * 소리가 없는 장치는 정확히 0을 보낸다.
 */
const SILENT_LEVEL = 0.0005;

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
  /** audio_end 뒤에 받아 둔 소리가 다 나가기를 기다리는 한도. 소리가 멈춘 채(뒤로 간 탭) 구슬만 말하는 모양으로 남지 않게 한다. */
  drainTimeoutMs: 30_000,
  /** 듣는 동안 마이크에서 아무 소리도 오지 않는 채로 이만큼 지나면 알린다. 잘못된 입력 장치(소리 없는 가상 마이크)가 흔한 까닭이다. */
  silentMicMs: 8_000,
  reconnectBaseMs: 1_000,
  reconnectMaxMs: 30_000,
} as const;

/** 같은 글을 저절로 다시 보내는 횟수의 한도. 넘으면 사용자에게 맡긴다. */
export const MAX_SEND_ATTEMPTS = 3;
/** 이어서 실패한 연결 시도가 이만큼이면 그만두고 사용자에게 알린다. */
export const MAX_RECONNECT_ATTEMPTS = 6;

type Timer = ReturnType<typeof setTimeout>;
type TimerName =
  | 'connectTimer'
  | 'reconnectTimer'
  | 'ackTimer'
  | 'replyTimer'
  | 'endTimer'
  | 'rateLimitTimer'
  | 'drainTimer'
  | 'silenceTimer';

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
 * 음성이 켜지면 마이크의 조각을 바이너리로 올려 보내고, 내려온 소리를 재생기에 넘긴다.
 * 화면은 subscribe와 getState로 상태만 읽는다. 리액트를 모른다.
 *
 * 받은 글과 보낸 글, 소리는 어디에도 기록하지 않는다. 오류를 만나도 내용을 콘솔에 찍지 않는다.
 */
export class ConversationClient {
  private state: ConversationState = initialConversationState;
  private readonly listeners = new Set<() => void>();
  private readonly levelListeners = new Set<(level: number) => void>();

  private readonly url: string;
  private readonly createSocket: (url: string) => SocketLike;
  private readonly createCapture: () => CaptureLike;
  private readonly createPlayer: () => PlayerLike;
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

  /** 사용자가 음성으로 이야기하고 싶어 하는지. 마이크가 켜지는 중이거나 연결이 끊겨 있어도 참일 수 있다. */
  private voiceWanted = false;
  private capture: CaptureLike | null = null;
  private player: PlayerLike | null = null;
  /** audio_end(done)를 받았고 받아 둔 소리가 다 나가기를 기다리는 말의 순번 */
  private drainingSeq: number | null = null;

  private connectTimer: Timer | null = null;
  private reconnectTimer: Timer | null = null;
  private ackTimer: Timer | null = null;
  private replyTimer: Timer | null = null;
  private endTimer: Timer | null = null;
  private rateLimitTimer: Timer | null = null;
  private drainTimer: Timer | null = null;
  private silenceTimer: Timer | null = null;

  constructor(options: ConversationClientOptions) {
    this.url = options.url;
    this.createSocket = options.createSocket ?? ((url) => new WebSocket(url));
    this.createCapture = options.createCapture ?? (() => new MicCapture());
    this.createPlayer = options.createPlayer ?? (() => new AudioPlayer());
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

  /**
   * 소리 크기(0~1)를 받는다. 듣는 동안은 마이크, 내일이 말하는 동안은 재생의 크기다.
   * 초당 수십 번 오므로 상태를 거치지 않는다. 화면은 이 값을 구슬의 CSS 변수에 바로 넣는다.
   */
  readonly subscribeLevel = (listener: (level: number) => void): (() => void) => {
    this.levelListeners.add(listener);
    return () => this.levelListeners.delete(listener);
  };

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

  /** 화면을 떠날 때 부른다. 대화는 서버에 열린 채로 남고, 돌아와서 start를 부르면 이어진다. 마이크는 놓는다. */
  stop(): void {
    this.stopped = true;
    this.clearAllTimers();
    this.releaseAudio();
    this.closeSocket();
  }

  // ---- 음성 -------------------------------------------------------------------------

  /**
   * 음성으로 이야기하기 시작한다. 사용자 동작(탭) 안에서 불러야 한다. iOS는 그 안에서 연 AudioContext만 소리를 낸다.
   * 마이크가 켜지면 서버에 음성 방식을 청한다. 연결 전이면 start에, 연결 뒤면 set_mode에 실린다.
   * 거절되거나 마이크가 없으면 상태와 알림으로 드러난다. 던지지 않는다.
   */
  startVoice(): Promise<void> {
    if (this.state.phase === 'ended' || this.state.ending) return Promise.resolve();
    if (this.state.mic === 'on' || this.state.mic === 'starting') return Promise.resolve();

    this.voiceWanted = true;
    rememberPreferredMode('voice');
    this.dispatch({ type: 'mic', mic: 'starting' });

    const capture = this.captureFor();
    return capture
      .start({
        onFrame: (frame) => this.handleMicFrame(frame),
        onLevel: (level) => this.handleMicLevel(level),
      })
      .then(
        () => {
          if (!this.voiceWanted) {
            // 기다리는 사이에 껐다.
            capture.stop();
            return;
          }
          this.dispatch({ type: 'mic', mic: 'on' });
          this.syncMode();
        },
        (error: unknown) => {
          if (!this.voiceWanted) return;
          this.voiceWanted = false;
          this.emitLevel(0);
          this.dispatch({ type: 'mic_failed', reason: micFailureOf(error) });
        },
      );
  }

  /** 음성을 끄고 글로 이어간다. 마이크를 놓고 서버에 글 방식을 청한다. 재생 중인 말은 끝까지 들려준다. */
  stopVoice(): void {
    this.voiceWanted = false;
    rememberPreferredMode('chat');
    this.releaseMic();
    this.syncMode();
  }

  /** 내일의 말을 끊는다. 받아 둔 소리는 바로 버리고 서버에도 알린다. */
  interrupt(): void {
    const speaking = this.state.speaking;
    this.player?.clear();
    this.clear('drainTimer');
    this.drainingSeq = null;
    this.emitLevel(0);
    if (speaking !== null) this.dispatch({ type: 'playback_finished', seq: speaking.seq });
    if (this.state.phase === 'ready') this.sendFrame({ type: 'interrupt' });
    // 끊었다는 것은 말하고 싶다는 뜻이다. 바로 듣기를 청한다.
    this.listen();
  }

  /**
   * 한 마디 듣기를 청한다. 서버는 다음 끝점까지 듣고 스스로 멈춘다.
   * 마이크가 켜지고 서버가 음성으로 바꿔 준 뒤에 한 번 저절로 청하고, 그 뒤로는 사용자가 구슬을 누를 때마다 청한다.
   */
  listen(): void {
    if (this.state.phase !== 'ready' || this.state.mode !== 'voice' || this.state.mic !== 'on')
      return;
    if (this.state.listening) return;
    this.sendFrame({ type: 'listen', active: true });
  }

  /** "다 말했어요". 끝점을 기다리지 않고 지금까지 알아들은 말을 확정한다. */
  finalize(): void {
    if (this.state.phase !== 'ready' || this.state.mode !== 'voice') return;
    this.sendFrame({ type: 'finalize' });
  }

  private captureFor(): CaptureLike {
    this.capture ??= this.createCapture();
    return this.capture;
  }

  private playerFor(): PlayerLike {
    if (this.player === null) {
      const player = this.createPlayer();
      player.onLevel = (level) => {
        if (this.state.speaking !== null) this.emitLevel(level);
      };
      player.onDrained = () => this.handleDrained();
      this.player = player;
    }
    return this.player;
  }

  /** 서버가 아는 방식을 사용자가 바라는 쪽에 맞춘다. 마이크가 켜진 뒤에만 음성을 청한다. */
  private syncMode(): void {
    if (this.state.phase !== 'ready') return;
    if (this.voiceWanted) {
      if (this.state.mic === 'on' && this.state.mode !== 'voice') {
        this.sendFrame({ type: 'set_mode', mode: 'voice' });
      }
    } else if (this.state.mode === 'voice') {
      this.sendFrame({ type: 'set_mode', mode: 'chat' });
    }
  }

  private handleMicFrame(frame: ArrayBuffer): void {
    // 서버는 음성 방식으로 듣고 있을 때만 소리를 받는다. 그 전에 보내면 거절되거나 버려진다.
    if (this.socket === null || this.state.phase !== 'ready' || this.state.mode !== 'voice') return;
    if (!this.state.listening) return;
    try {
      this.socket.send(frame);
    } catch {
      this.dropSocket();
    }
  }

  private handleMicLevel(level: number): void {
    if (this.state.mic !== 'on') return;
    if (!this.state.listening) {
      // 듣지 않는 동안의 소리는 구슬에도 보이지 않는다. 멈춘 마이크처럼 보여야 한다.
      this.clear('silenceTimer');
      return;
    }
    if (this.state.speaking === null) this.emitLevel(level);
    this.watchSilence(level);
  }

  /**
   * 마이크가 열렸는데 소리가 전혀 없는지 본다. 켜진 가상 마이크나 꺼진 입력 장치는 권한도 받고 트랙도 살아 있는 채로 무음만 보낸다.
   * 그러면 "듣고 있어요"만 끝없이 이어지고 사용자는 까닭을 알 수 없다. 사용자의 차례(내일이 말하지도, 답을 준비하지도 않는 때)에만 잰다.
   */
  private watchSilence(level: number): void {
    if (level > SILENT_LEVEL) {
      this.clear('silenceTimer');
      if (this.state.notice === 'mic_silent') this.dispatch({ type: 'mic_sound' });
      return;
    }
    if (this.state.speaking !== null || this.state.awaitingReply) {
      this.clear('silenceTimer');
      return;
    }
    if (this.silenceTimer !== null || this.state.notice === 'mic_silent') return;
    this.silenceTimer = setTimeout(() => {
      this.silenceTimer = null;
      this.dispatch({ type: 'mic_silent' });
    }, TIMING.silentMicMs);
  }

  private handleAudioFrame(frame: ArrayBuffer): void {
    // audio_start 없이 온 소리는 어느 말인지 모른다. 버린다.
    if (this.state.speaking === null || this.player === null) return;
    this.player.push(frame);
  }

  private handleDrained(): void {
    this.emitLevel(0);
    if (this.drainingSeq === null) return;
    const seq = this.drainingSeq;
    this.drainingSeq = null;
    this.clear('drainTimer');
    this.dispatch({ type: 'playback_finished', seq });
  }

  /** 서버가 음성을 열지 못했거나 더 이어갈 수 없다. 마이크를 놓고 소리를 비운다. 상태는 서버 메시지의 몫이다. */
  private voiceLost(): void {
    this.voiceWanted = false;
    this.capture?.stop();
    this.player?.clear();
    this.clear('drainTimer');
    this.drainingSeq = null;
    this.emitLevel(0);
  }

  private releaseMic(): void {
    this.capture?.stop();
    this.clear('silenceTimer');
    this.emitLevel(0);
    if (this.state.mic !== 'off') this.dispatch({ type: 'mic', mic: 'off' });
  }

  /** 마이크와 재생기를 모두 놓는다. 대화가 끝났거나 화면을 떠날 때. */
  private releaseAudio(): void {
    this.voiceWanted = false;
    this.drainingSeq = null;
    this.player?.stop();
    this.releaseMic();
  }

  private emitLevel(level: number): void {
    for (const listener of this.levelListeners) listener(level);
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
    socket.binaryType = 'arraybuffer';

    socket.onopen = () => {
      if (this.socket !== socket) return;
      this.opened = true;
      // 음성으로 이야기하던 중에 다시 잇는 것이면 처음부터 음성으로 연다. 쓸 수 없으면 서버가 알리고 글로 연다.
      this.sendFrame({ type: 'start', mode: this.voiceWanted ? 'voice' : 'chat' });
    };
    socket.onmessage = (event) => {
      if (this.socket !== socket) return;
      const data: unknown = event.data;
      if (data instanceof ArrayBuffer) {
        this.handleAudioFrame(data);
        return;
      }
      const message = parseServerMessage(data);
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
    this.clear('drainTimer');
    // 소리는 연결과 함께 끊겼다. 남은 조각을 들려줘 봐야 말의 토막이다.
    this.drainingSeq = null;
    this.player?.clear();
    if (this.stopped || this.state.phase === 'ended') return;

    if (this.endSent) {
      // 끝내기는 나갔는데 답을 듣지 못했다. 다시 연결하면 서버가 새 대화를 열어 버린다.
      // 끝난 것으로 보고, 일기가 준비됐는지는 화면이 직접 확인하게 둔다.
      this.releaseAudio();
      this.dispatch({ type: 'ended_unconfirmed' });
      return;
    }

    if (code === CLOSE_CODE.takenOver) {
      // 다른 화면이 대화를 이어받았다. 여기서 다시 이으면 그 화면에서 다시 빼앗아 오고,
      // 두 화면이 끝없이 대화를 주고받는다. 이어가려면 사용자가 고르게 한다. 마이크도 그 화면의 몫이다.
      this.releaseAudio();
      this.dispatch({ type: 'taken_over' });
      return;
    }
    if (code === CLOSE_CODE.gone) {
      // 계정이 사라졌다. 다시 이어도 같은 까닭으로 닫힌다. 로그인 상태를 확인하면 경로 보호가 데리고 나간다.
      this.onHandshakeFailed?.();
      this.releaseAudio();
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
        // 다시 이었을 때 서버가 연 방식이 바라는 것과 다르면 맞춘다. 이미 음성으로 열렸으면 바로 한 마디를 듣는다.
        this.syncMode();
        this.listen();
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
        this.releaseAudio();
        // 일기가 준비됐다는 소식을 들으려고 연결은 열어 둔다. 기다릴 것이 없으면 닫는다.
        if (!message.diary_expected) this.closeSocket();
        break;

      case 'diary_ready':
        this.closeSocket();
        break;

      case 'error':
        this.handleServerError(message);
        break;

      case 'mode':
        if (message.mode === 'chat' && this.voiceWanted) {
          // 바라지 않았는데 글로 내려왔다. 음성이 죽은 것이다.
          this.voiceLost();
          this.dispatch({ type: 'voice_dropped' });
        } else {
          this.syncMode();
          // 음성이 열렸다. 첫 한 마디는 바로 듣는다. 사용자는 이미 시작하려고 눌렀다.
          this.listen();
        }
        break;

      case 'audio_start': {
        this.clear('drainTimer');
        this.drainingSeq = null;
        const { seq } = message;
        this.playerFor()
          .start(message.sample_rate)
          .catch(() => {
            // 재생기를 열지 못했다. 글은 이미 화면에 있으니 소리만 건너뛴다.
            this.dispatch({ type: 'playback_finished', seq });
          });
        break;
      }

      case 'audio_end':
        this.handleAudioEnd(message);
        break;

      case 'transcript':
      case 'resources':
        break;
    }
  }

  private handleAudioEnd(message: Extract<ServerMessage, { type: 'audio_end' }>): void {
    if (message.reason !== 'done') {
      // 끊겼거나 만들지 못했다. 받아 둔 조각은 말의 토막이라 들려주지 않는다.
      this.player?.clear();
      this.clear('drainTimer');
      this.drainingSeq = null;
      this.emitLevel(0);
      return;
    }
    if (this.player?.isPlaying() !== true) {
      this.dispatch({ type: 'playback_finished', seq: message.seq });
      return;
    }
    // 끝까지 받았다. 받아 둔 소리가 다 나가면 재생기가 알린다.
    this.drainingSeq = message.seq;
    this.clear('drainTimer');
    this.drainTimer = setTimeout(() => {
      this.drainTimer = null;
      this.handleDrained();
    }, TIMING.drainTimeoutMs);
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
        this.releaseAudio();
        this.closeSocket();
        break;
      case 'voice_unavailable':
        // 글로 이어진다. 보내 둔 글의 타이머는 그대로 둔다.
        this.voiceLost();
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

  /** 텍스트 프레임 하나를 보낸다. 보내지 못했으면 연결을 버리고 false를 돌려준다. */
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

  private clear(name: TimerName): void {
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
    this.clear('drainTimer');
  }
}
