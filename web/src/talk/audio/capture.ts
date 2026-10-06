import {
  browserAudio,
  type AudioNodeLike,
  type AudioSystem,
  type WorkletNodeLike,
} from '@/talk/audio/audioContext';
import type { MicFailure } from '@/talk/conversationState';

/** 워클릿 파일의 주소. public/ 아래의 평범한 JS라 번들러를 거치지 않고, 같은 출처에서 그대로 받는다. */
export const CAPTURE_WORKLET_URL = '/audio/capture-worklet.js';
/** 워클릿 파일이 registerProcessor에 적은 이름과 같아야 한다. */
export const CAPTURE_PROCESSOR = 'naeil-capture';
/** 서버가 받는 소리의 샘플레이트(Hz). 워클릿이 이 값으로 내려 보낸다. */
export const CAPTURE_SAMPLE_RATE = 16_000;
/** 조각 하나의 크기. 16kHz 16비트 모노의 100ms다. 워클릿의 값과 같아야 한다. */
export const CAPTURE_FRAME_BYTES = (CAPTURE_SAMPLE_RATE / 10) * 2;

/** 에코 제거와 잡음 억제는 브라우저에 맡긴다. 스피커로 나가는 내일의 말이 마이크로 되돌아오는 것을 줄인다. */
const CONSTRAINTS: MediaStreamConstraints = {
  audio: {
    echoCancellation: true,
    noiseSuppression: true,
    autoGainControl: true,
    channelCount: 1,
  },
};

export interface CaptureCallbacks {
  /** 100ms 조각 하나(PCM s16le 16kHz 모노, 3,200바이트) */
  onFrame: (frame: ArrayBuffer) => void;
  /** 소리 크기(0~1). 구슬이 따라 움직인다. */
  onLevel: (level: number) => void;
}

/** ConversationClient가 마이크에서 쓰는 부분. 테스트는 같은 모양의 대역을 끼운다. */
export interface CaptureLike {
  start(callbacks: CaptureCallbacks): Promise<void>;
  stop(): void;
}

/** 마이크를 켜지 못했다. reason으로 안내 문구가 갈린다. */
export class MicError extends Error {
  readonly reason: MicFailure;

  constructor(reason: MicFailure) {
    super(`microphone ${reason}`);
    this.name = 'MicError';
    this.reason = reason;
  }
}

/** 브라우저가 던진 오류를 안내 문구의 갈래로 바꾼다. 오류의 내용은 어디에도 남기지 않는다. */
export function micFailureOf(error: unknown): MicFailure {
  if (error instanceof MicError) return error.reason;
  const name =
    typeof error === 'object' && error !== null && 'name' in error && typeof error.name === 'string'
      ? error.name
      : '';
  switch (name) {
    case 'NotAllowedError':
    case 'PermissionDeniedError':
    case 'SecurityError':
      return 'denied';
    case 'NotFoundError':
    case 'DevicesNotFoundError':
    case 'OverconstrainedError':
      return 'missing';
    default:
      return 'failed';
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

/**
 * 마이크 → 워클릿. 워클릿이 16kHz 16비트 모노 100ms 조각과 소리 크기를 돌려준다.
 * 소리의 내용은 어디에도 기록하지 않는다.
 */
export class MicCapture implements CaptureLike {
  private readonly audio: AudioSystem;
  private stream: MediaStream | null = null;
  private source: AudioNodeLike | null = null;
  private node: WorkletNodeLike | null = null;
  /** start가 기다리는 동안 stop이 불렸는지 알아보려고 센다. */
  private generation = 0;

  constructor(audio: AudioSystem = browserAudio) {
    this.audio = audio;
  }

  /**
   * 마이크를 켠다. 사용자 동작 안에서 불러야 한다(iOS는 그 안에서 만든 AudioContext만 소리를 낸다).
   * 권한이 거절됐거나 마이크가 없으면 MicError를 던진다.
   */
  async start(callbacks: CaptureCallbacks): Promise<void> {
    this.stop();
    if (!this.audio.isSecureContext()) {
      // 이 주소에서는 브라우저가 마이크를 내주지 않는다. 권한을 묻기도 전의 일이라 그 사실을 따로 알린다.
      throw new MicError('insecure');
    }
    const generation = this.generation;
    // 컨텍스트는 await보다 먼저, 동작 처리기 안에서 만든다.
    const context = this.audio.context();

    // 워클릿을 먼저 싣는다. 받지 못하면 마이크 권한을 물을 까닭이 없다.
    try {
      await this.audio.loadWorklet(context, CAPTURE_WORKLET_URL);
    } catch {
      throw new MicError('failed');
    }
    if (generation !== this.generation) return;

    let stream: MediaStream;
    try {
      stream = await this.audio.getUserMedia(CONSTRAINTS);
    } catch (error) {
      throw new MicError(micFailureOf(error));
    }
    if (generation !== this.generation) {
      // 기다리는 사이에 stop이 불렸다. 빨간 마이크 표시가 남지 않게 트랙을 바로 놓는다.
      releaseTracks(stream);
      return;
    }

    let node: WorkletNodeLike;
    let source: AudioNodeLike;
    try {
      node = this.audio.createWorkletNode(context, CAPTURE_PROCESSOR, {
        numberOfInputs: 1,
        numberOfOutputs: 1,
        outputChannelCount: [1],
      });
      source = context.createMediaStreamSource(stream);
    } catch {
      releaseTracks(stream);
      throw new MicError('failed');
    }

    node.port.onmessage = (event) => {
      const data: unknown = event.data;
      if (!isRecord(data)) return;
      if (data.type === 'frame' && data.buffer instanceof ArrayBuffer) {
        callbacks.onFrame(data.buffer);
      } else if (data.type === 'level' && typeof data.value === 'number') {
        callbacks.onLevel(data.value);
      }
    };
    source.connect(node);
    // 워클릿은 무음을 내보낸다. 그래도 목적지까지 이어 두는 까닭은, 브라우저가 목적지에서 거슬러 올라가며
    // 노드를 돌리기 때문이다. 이어 두지 않으면 process가 불리지 않는 브라우저가 있다.
    node.connect(context.destination);

    this.stream = stream;
    this.source = source;
    this.node = node;
  }

  stop(): void {
    this.generation += 1;
    if (this.node !== null) {
      this.node.port.onmessage = null;
      this.node.disconnect();
      this.node = null;
    }
    if (this.source !== null) {
      this.source.disconnect();
      this.source = null;
    }
    if (this.stream !== null) {
      releaseTracks(this.stream);
      this.stream = null;
    }
  }
}

function releaseTracks(stream: MediaStream): void {
  for (const track of stream.getTracks()) track.stop();
}
