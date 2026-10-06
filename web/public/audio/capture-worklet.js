/* global AudioWorkletProcessor, registerProcessor, sampleRate */

/**
 * 마이크 소리(컨텍스트 샘플레이트의 Float32 모노)를 16kHz 16비트 모노로 바꿔 100ms 조각으로 넘긴다.
 *
 * 번들러를 거치지 않는 평범한 모듈이다. 운영 서버의 스크립트 정책(script-src 'self')을 그대로 통과한다.
 * 오디오 스레드에서 돈다. 소리의 내용은 어디에도 기록하지 않는다.
 *
 * 받는 메시지: { type: 'mute', value: boolean } — 켜져 있는 동안은 아무것도 보내지 않는다.
 * 보내는 메시지: { type: 'frame', buffer } (3,200바이트, 소유권을 넘긴다), { type: 'level', value: 0~1 }
 */

const TARGET_RATE = 16000;
/** 조각 하나의 샘플 수. 100ms다. 서버가 받는 프레임 크기(3,200바이트)와 같아야 한다. */
const FRAME_SAMPLES = TARGET_RATE / 10;
/** 소리 크기를 알리는 간격(입력 샘플 수). 약 50ms마다 한 번이다. */
const LEVEL_INTERVAL = Math.round(sampleRate / 20);

function toInt16(sample) {
  const clamped = Math.max(-1, Math.min(1, sample));
  return clamped < 0 ? Math.round(clamped * 32768) : Math.round(clamped * 32767);
}

class CaptureProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    /** 입력 샘플 몇 개마다 출력 샘플 하나를 만드는지. 48kHz면 3이다. */
    this.ratio = sampleRate / TARGET_RATE;
    /** 다음에 읽을 입력 위치. 지금 블록의 첫 샘플이 0이고, 음수는 지난 블록의 마지막 샘플 쪽이다. */
    this.position = 0;
    /** 지난 블록의 마지막 샘플. 블록 경계에서도 끊기지 않고 보간하려고 둔다. */
    this.last = 0;
    this.frame = new Int16Array(FRAME_SAMPLES);
    this.filled = 0;
    this.muted = false;
    this.squares = 0;
    this.counted = 0;

    this.port.onmessage = (event) => {
      const data = event.data;
      if (data && data.type === 'mute') this.muted = data.value === true;
    };
  }

  process(inputs) {
    const input = inputs[0];
    const channel = input && input[0];
    // 입력이 잠시 끊겨도 노드를 살려 둔다. 트랙이 돌아오면 다시 이어진다.
    if (!channel || channel.length === 0) return true;

    if (this.muted) {
      this.reset();
      return true;
    }
    this.measure(channel);
    this.resample(channel);
    return true;
  }

  reset() {
    this.position = 0;
    this.last = 0;
    this.filled = 0;
    this.squares = 0;
    this.counted = 0;
  }

  measure(channel) {
    for (let i = 0; i < channel.length; i += 1) this.squares += channel[i] * channel[i];
    this.counted += channel.length;
    if (this.counted < LEVEL_INTERVAL) return;

    const rms = Math.sqrt(this.squares / this.counted);
    // 보통 크기로 말하면 0.1~0.3쯤이다. 구슬이 눈에 띄게 따라오도록 끌어올리되 1을 넘기지 않는다.
    this.port.postMessage({ type: 'level', value: Math.min(1, rms * 3) });
    this.squares = 0;
    this.counted = 0;
  }

  /** 선형 보간으로 16kHz까지 내린다. 조각이 차면 바로 넘긴다. */
  resample(channel) {
    const length = channel.length;
    let position = this.position;
    while (position < length - 1) {
      const index = Math.floor(position);
      const fraction = position - index;
      const before = index < 0 ? this.last : channel[index];
      const after = channel[index + 1];
      this.frame[this.filled] = toInt16(before + (after - before) * fraction);
      this.filled += 1;
      if (this.filled === FRAME_SAMPLES) this.flush();
      position += this.ratio;
    }
    this.position = position - length;
    this.last = channel[length - 1];
  }

  flush() {
    const buffer = this.frame.buffer;
    // 복사하지 않고 소유권을 넘긴다. 넘긴 뒤에는 쓸 수 없으므로 새 조각을 잡는다.
    this.port.postMessage({ type: 'frame', buffer }, [buffer]);
    this.frame = new Int16Array(FRAME_SAMPLES);
    this.filled = 0;
  }
}

registerProcessor('naeil-capture', CaptureProcessor);
