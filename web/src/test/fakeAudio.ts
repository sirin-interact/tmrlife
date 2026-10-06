import { onTestFinished, vi } from 'vitest';

import type {
  AudioContextLike,
  AudioNodeLike,
  AudioSystem,
  WorkletNodeLike,
  WorkletNodeOptions,
  WorkletPortLike,
} from '@/talk/audio/audioContext';
import { CAPTURE_FRAME_BYTES, type CaptureCallbacks, type CaptureLike } from '@/talk/audio/capture';
import type { PlayerLike } from '@/talk/audio/player';

// ---- ConversationClient에 끼우는 대역 ---------------------------------------------

/** 마이크 대역. 테스트가 조각과 소리 크기를 직접 흘려 넣는다. */
export class FakeCapture implements CaptureLike {
  private callbacks: CaptureCallbacks | null = null;
  starts = 0;
  stops = 0;
  /** 정해 두면 start가 이 값을 던진다. 권한 거절과 마이크 없음을 흉내 낸다. */
  failWith: Error | null = null;

  start(callbacks: CaptureCallbacks): Promise<void> {
    this.starts += 1;
    if (this.failWith !== null) return Promise.reject(this.failWith);
    this.callbacks = callbacks;
    return Promise.resolve();
  }

  stop(): void {
    this.stops += 1;
    this.callbacks = null;
  }

  get on(): boolean {
    return this.callbacks !== null;
  }

  /** 워클릿이 100ms 조각 하나를 넘겼다. */
  frame(bytes = CAPTURE_FRAME_BYTES): ArrayBuffer {
    const buffer = new ArrayBuffer(bytes);
    this.callbacks?.onFrame(buffer);
    return buffer;
  }

  level(value: number): void {
    this.callbacks?.onLevel(value);
  }
}

/** 재생기 대역. 받은 조각을 모아 두고, 테스트가 다 들려줬다고 알린다. */
export class FakePlayer implements PlayerLike {
  onLevel: ((level: number) => void) | null = null;
  onDrained: (() => void) | null = null;
  readonly started: number[] = [];
  readonly pushed: ArrayBuffer[] = [];
  clears = 0;
  stops = 0;
  private playing = false;

  start(sampleRate: number): Promise<void> {
    this.started.push(sampleRate);
    return Promise.resolve();
  }

  push(chunk: ArrayBuffer): void {
    this.pushed.push(chunk);
    this.playing = true;
  }

  clear(): void {
    this.clears += 1;
    this.playing = false;
  }

  isPlaying(): boolean {
    return this.playing;
  }

  stop(): void {
    this.stops += 1;
    this.clear();
  }

  /** 쌓아 둔 소리를 다 내보냈다. */
  drain(): void {
    this.playing = false;
    this.onDrained?.();
  }

  level(value: number): void {
    this.onLevel?.(value);
  }
}

// ---- 캡처와 재생기가 쓰는 Web Audio 대역 ------------------------------------------

export class FakeNode implements AudioNodeLike {
  readonly connections: AudioNodeLike[] = [];
  disconnected = 0;

  connect(destination: AudioNodeLike): AudioNodeLike {
    this.connections.push(destination);
    return destination;
  }

  disconnect(): void {
    this.disconnected += 1;
  }
}

export class FakePort implements WorkletPortLike {
  onmessage: ((event: MessageEvent) => void) | null = null;
  /** 메인 스레드가 워클릿으로 보낸 메시지 */
  readonly posted: Array<{ message: unknown; transfer: Transferable[] | undefined }> = [];

  postMessage(message: unknown, transfer?: Transferable[]): void {
    this.posted.push({ message, transfer });
  }

  /** 워클릿이 메인 스레드로 보낸 메시지를 흉내 낸다. */
  emit(message: unknown): void {
    this.onmessage?.(new MessageEvent('message', { data: message }));
  }

  postedOfType(type: string): unknown[] {
    return this.posted
      .map((item) => item.message)
      .filter((message) => (message as { type?: unknown }).type === type);
  }
}

export class FakeWorkletNode extends FakeNode implements WorkletNodeLike {
  readonly port = new FakePort();
  readonly context: AudioContextLike;
  readonly name: string;
  readonly options: WorkletNodeOptions;

  constructor(context: AudioContextLike, name: string, options: WorkletNodeOptions) {
    super();
    this.context = context;
    this.name = name;
    this.options = options;
  }
}

export class FakeAudioContext implements AudioContextLike {
  readonly sampleRate = 48_000;
  readonly state = 'running';
  readonly destination = new FakeNode();
  readonly sources: FakeNode[] = [];
  readonly modules: string[] = [];
  resumed = 0;
  readonly audioWorklet = {
    addModule: (url: string): Promise<void> => {
      this.modules.push(url);
      return Promise.resolve();
    },
  };

  resume(): Promise<void> {
    this.resumed += 1;
    return Promise.resolve();
  }

  createMediaStreamSource(): AudioNodeLike {
    const source = new FakeNode();
    this.sources.push(source);
    return source;
  }
}

export interface FakeTrack {
  stop: () => void;
  stopped: boolean;
}

export function fakeStream(): MediaStream & { tracks: FakeTrack[] } {
  const track: FakeTrack = {
    stopped: false,
    stop() {
      this.stopped = true;
    },
  };
  const tracks = [track];
  return { tracks, getTracks: () => tracks } as unknown as MediaStream & { tracks: FakeTrack[] };
}

export interface FakeAudioSystem extends AudioSystem {
  readonly context: () => FakeAudioContext;
  readonly nodes: FakeWorkletNode[];
  readonly streams: ReturnType<typeof fakeStream>[];
  /** 정해 두면 getUserMedia가 이 값을 던진다. */
  denyWith: Error | null;
  /** 정해 두면 워클릿을 싣지 못한다. */
  moduleError: Error | null;
  /** 거짓이면 안전하지 않은 주소다. 마이크를 열기 전에 막힌다. */
  secure: boolean;
  /** 지금까지 getUserMedia에 넘긴 조건 */
  readonly constraints: MediaStreamConstraints[];
  latestNode: (name: string) => FakeWorkletNode;
}

/** 캡처와 재생기 테스트에 끼우는 Web Audio 대역. 컨텍스트는 하나다. */
export function fakeAudioSystem(): FakeAudioSystem {
  const context = new FakeAudioContext();
  const nodes: FakeWorkletNode[] = [];
  const streams: ReturnType<typeof fakeStream>[] = [];
  const constraints: MediaStreamConstraints[] = [];
  const system: FakeAudioSystem = {
    nodes,
    streams,
    constraints,
    denyWith: null,
    moduleError: null,
    secure: true,
    isSecureContext: () => system.secure,
    context: () => context,
    loadWorklet: (ctx, url) => {
      if (system.moduleError !== null) return Promise.reject(system.moduleError);
      return ctx.audioWorklet.addModule(url);
    },
    createWorkletNode: (ctx, name, options) => {
      const node = new FakeWorkletNode(ctx, name, options);
      nodes.push(node);
      return node;
    },
    getUserMedia: (requested) => {
      constraints.push(requested);
      if (system.denyWith !== null) return Promise.reject(system.denyWith);
      const stream = fakeStream();
      streams.push(stream);
      return Promise.resolve(stream);
    },
    latestNode: (name) => {
      const node = nodes.filter((item) => item.name === name).at(-1);
      if (!node) throw new Error(`${name} 노드를 아직 만들지 않았다`);
      return node;
    },
  };
  return system;
}

export interface FakeWebAudio {
  readonly nodes: FakeWorkletNode[];
  /** 워클릿 이름으로 가장 최근에 만든 노드 */
  latestNode: (name: string) => FakeWorkletNode;
  getUserMedia: ReturnType<
    typeof vi.fn<(constraints: MediaStreamConstraints) => Promise<MediaStream>>
  >;
}

/**
 * 브라우저 전역의 Web Audio(AudioContext, AudioWorkletNode, navigator.mediaDevices)를 대역으로 바꿔 끼운다.
 * 화면 테스트에서 쓴다. 테스트가 끝나면 되돌린다.
 */
export function installFakeWebAudio(
  options: { getUserMedia?: (constraints: MediaStreamConstraints) => Promise<MediaStream> } = {},
): FakeWebAudio {
  const nodes: FakeWorkletNode[] = [];
  vi.stubGlobal('AudioContext', FakeAudioContext);
  vi.stubGlobal(
    'AudioWorkletNode',
    class extends FakeWorkletNode {
      constructor(context: AudioContextLike, name: string, workletOptions: WorkletNodeOptions) {
        super(context, name, workletOptions);
        nodes.push(this);
      }
    },
  );

  const getUserMedia = vi.fn(options.getUserMedia ?? (() => Promise.resolve(fakeStream())));
  Object.defineProperty(navigator, 'mediaDevices', {
    configurable: true,
    value: { getUserMedia },
  });
  onTestFinished(() => {
    delete (navigator as { mediaDevices?: unknown }).mediaDevices;
  });

  return {
    nodes,
    getUserMedia,
    latestNode: (name) => {
      const node = nodes.filter((item) => item.name === name).at(-1);
      if (!node) throw new Error(`${name} 노드를 아직 만들지 않았다`);
      return node;
    },
  };
}

/** 브라우저가 마이크 권한을 거절했을 때 던지는 오류 */
export function notAllowedError(): DOMException {
  return new DOMException('Permission denied', 'NotAllowedError');
}

/** 마이크가 없을 때 던지는 오류 */
export function notFoundError(): DOMException {
  return new DOMException('Requested device not found', 'NotFoundError');
}
