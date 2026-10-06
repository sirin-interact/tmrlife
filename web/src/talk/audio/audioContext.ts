/**
 * 마이크 캡처와 재생이 함께 쓰는 AudioContext와, 그 둘이 브라우저에서 쓰는 부분만 추린 경계.
 *
 * jsdom에는 Web Audio가 없다. 캡처와 재생은 아래 인터페이스만 보고, 실제 브라우저 객체는 browserAudio가 만든다.
 * 테스트는 같은 모양의 대역을 끼운다.
 */

export interface AudioNodeLike {
  connect(destination: AudioNodeLike): unknown;
  disconnect(): void;
}

export interface WorkletPortLike {
  postMessage(message: unknown, transfer?: Transferable[]): void;
  onmessage: ((event: MessageEvent) => void) | null;
}

export interface WorkletNodeLike extends AudioNodeLike {
  readonly port: WorkletPortLike;
}

export interface WorkletNodeOptions {
  numberOfInputs: number;
  numberOfOutputs: number;
  outputChannelCount: number[];
}

export interface AudioContextLike {
  readonly sampleRate: number;
  readonly destination: AudioNodeLike;
  readonly audioWorklet: { addModule(url: string): Promise<void> };
  resume(): Promise<void>;
  createMediaStreamSource(stream: MediaStream): AudioNodeLike;
}

export interface AudioSystem {
  /**
   * 마이크를 열 수 있는 출처인지. 브라우저는 https이거나 localhost인 페이지에서만 마이크와 오디오 워클릿을 내준다.
   * 같은 공유기의 다른 기기에서 http로 연 개발 서버가 대표적인 예외다.
   */
  isSecureContext(): boolean;
  /**
   * 공유 컨텍스트를 돌려준다. 없으면 만든다. 동기다.
   * iOS는 사용자 동작(탭) 안에서 만들거나 깨운 컨텍스트만 소리를 내므로, 동작 처리기가 await보다 먼저 불러야 한다.
   */
  context(): AudioContextLike;
  /** 워클릿 모듈을 컨텍스트에 싣는다. 같은 주소는 한 번만 싣는다. */
  loadWorklet(context: AudioContextLike, url: string): Promise<void>;
  createWorkletNode(
    context: AudioContextLike,
    name: string,
    options: WorkletNodeOptions,
  ): WorkletNodeLike;
  getUserMedia(constraints: MediaStreamConstraints): Promise<MediaStream>;
}

/** 브라우저의 Web Audio를 그대로 쓰는 구현. 컨텍스트는 앱에 하나다(브라우저 기본 샘플레이트). */
export function createBrowserAudio(): AudioSystem {
  let shared: AudioContext | null = null;
  const loaded = new WeakMap<AudioContextLike, Map<string, Promise<void>>>();

  return {
    isSecureContext() {
      // 값이 없는 환경(시험)은 막지 않는다. 막아야 할 곳은 브라우저가 거짓이라고 말해 주는 곳뿐이다.
      return window.isSecureContext !== false;
    },

    context() {
      if (shared === null || shared.state === 'closed') shared = new AudioContext();
      // 멈춰 있으면 깨운다. 결과는 기다리지 않는다. 사용자 동작 밖에서 불리면 브라우저가 거절할 수 있고, 그래도 다음 탭에서 다시 깨어난다.
      if (shared.state !== 'running') {
        shared.resume().catch(() => {
          // 깨우지 못한 까닭은 남기지 않는다. 소리가 나지 않을 뿐이다.
        });
      }
      return shared;
    },

    loadWorklet(context, url) {
      let modules = loaded.get(context);
      if (modules === undefined) {
        modules = new Map();
        loaded.set(context, modules);
      }
      let loading = modules.get(url);
      if (loading === undefined) {
        loading = context.audioWorklet.addModule(url);
        modules.set(url, loading);
        // 실패한 것은 기억하지 않는다. 다음에 다시 받아 본다.
        loading.catch(() => modules?.delete(url));
      }
      return loading;
    },

    createWorkletNode(context, name, options) {
      return new AudioWorkletNode(context as AudioContext, name, options);
    },

    getUserMedia(constraints) {
      // 안전하지 않은 출처(같은 공유기의 폰으로 연 개발 서버)에는 mediaDevices가 없다. 거절로 다룬다.
      const devices = navigator.mediaDevices as MediaDevices | undefined;
      if (devices === undefined) {
        return Promise.reject(new DOMException('mediaDevices unavailable', 'NotSupportedError'));
      }
      return devices.getUserMedia(constraints);
    },
  };
}

export const browserAudio: AudioSystem = createBrowserAudio();
