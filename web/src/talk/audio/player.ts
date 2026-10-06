import { browserAudio, type AudioSystem, type WorkletNodeLike } from '@/talk/audio/audioContext';
import { DEFAULT_PLAYBACK_SAMPLE_RATE } from '@/talk/messages';

/** 워클릿 파일의 주소. public/ 아래의 평범한 JS라 번들러를 거치지 않고, 같은 출처에서 그대로 받는다. */
export const PLAYER_WORKLET_URL = '/audio/player-worklet.js';
/** 워클릿 파일이 registerProcessor에 적은 이름과 같아야 한다. */
export const PLAYER_PROCESSOR = 'naeil-player';

/** ConversationClient가 재생기에서 쓰는 부분. 테스트는 같은 모양의 대역을 끼운다. */
export interface PlayerLike {
  /** 재생을 준비한다. 다시 불러도 된다. 샘플레이트가 바뀌면 그 값으로 이어서 받는다. */
  start(sampleRate: number): Promise<void>;
  /** 소리 조각(PCM s16le 모노)을 뒤에 붙인다. 버퍼는 워클릿으로 넘어가므로 다시 쓰지 않는다. */
  push(chunk: ArrayBuffer): void;
  /** 받아 둔 소리를 모두 버린다. 끼어들 때 쓴다. */
  clear(): void;
  /** 마지막으로 바닥난 뒤에 소리를 더 받았는지. 참이면 아직 들려줄 것이 남아 있다. */
  isPlaying(): boolean;
  stop(): void;
  onLevel: ((level: number) => void) | null;
  /** 받아 둔 소리를 다 들려줬다. */
  onDrained: (() => void) | null;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

/**
 * 워클릿 → 스피커. 서버가 내려보낸 PCM 조각을 워클릿의 링 버퍼에 쌓고, 워클릿이 컨텍스트의 샘플레이트로 올려 재생한다.
 * 준비가 끝나기 전에 온 조각은 들고 있다가 준비되면 순서대로 넘긴다.
 */
export class AudioPlayer implements PlayerLike {
  onLevel: ((level: number) => void) | null = null;
  onDrained: (() => void) | null = null;

  private readonly audio: AudioSystem;
  private node: WorkletNodeLike | null = null;
  private opening: Promise<void> | null = null;
  private sampleRate = DEFAULT_PLAYBACK_SAMPLE_RATE;
  /** 워클릿이 준비되기 전에 온 조각 */
  private pending: ArrayBuffer[] = [];
  private playing = false;
  private generation = 0;

  constructor(audio: AudioSystem = browserAudio) {
    this.audio = audio;
  }

  async start(sampleRate: number): Promise<void> {
    this.sampleRate = sampleRate;
    if (this.node !== null) {
      this.node.port.postMessage({ type: 'config', sampleRate });
      return;
    }
    if (this.opening === null) this.opening = this.open();
    await this.opening;
  }

  private async open(): Promise<void> {
    const generation = this.generation;
    const context = this.audio.context();
    try {
      await this.audio.loadWorklet(context, PLAYER_WORKLET_URL);
    } catch (error) {
      this.opening = null;
      this.pending = [];
      this.playing = false;
      throw error;
    }
    if (generation !== this.generation) return;

    const node = this.audio.createWorkletNode(context, PLAYER_PROCESSOR, {
      numberOfInputs: 0,
      numberOfOutputs: 1,
      outputChannelCount: [1],
    });
    node.port.onmessage = (event) => {
      const data: unknown = event.data;
      if (!isRecord(data)) return;
      if (data.type === 'level' && typeof data.value === 'number') {
        this.onLevel?.(data.value);
      } else if (data.type === 'drained') {
        this.playing = false;
        this.onDrained?.();
      }
    };
    node.connect(context.destination);
    node.port.postMessage({ type: 'config', sampleRate: this.sampleRate });
    this.node = node;

    const queued = this.pending;
    this.pending = [];
    for (const chunk of queued) this.transfer(node, chunk);
  }

  push(chunk: ArrayBuffer): void {
    if (chunk.byteLength === 0) return;
    if (this.node !== null) {
      this.playing = true;
      this.transfer(this.node, chunk);
    } else if (this.opening !== null) {
      this.playing = true;
      this.pending.push(chunk);
    }
    // start 전에 온 조각은 들려줄 수 없다. 버린다.
  }

  clear(): void {
    this.pending = [];
    this.playing = false;
    this.node?.port.postMessage({ type: 'clear' });
  }

  isPlaying(): boolean {
    return this.playing;
  }

  stop(): void {
    this.generation += 1;
    this.clear();
    if (this.node !== null) {
      this.node.port.onmessage = null;
      this.node.disconnect();
      this.node = null;
    }
    this.opening = null;
  }

  private transfer(node: WorkletNodeLike, chunk: ArrayBuffer): void {
    node.port.postMessage({ type: 'push', buffer: chunk }, [chunk]);
  }
}
