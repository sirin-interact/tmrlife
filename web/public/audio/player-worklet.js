/* global AudioWorkletProcessor, registerProcessor, sampleRate */

/**
 * 서버가 내려보낸 16비트 모노 PCM 조각을 링 버퍼에 쌓아 두고, 컨텍스트의 샘플레이트로 올려 재생한다.
 *
 * 번들러를 거치지 않는 평범한 모듈이다. 운영 서버의 스크립트 정책(script-src 'self')을 그대로 통과한다.
 * 오디오 스레드에서 돈다. 소리의 내용은 어디에도 기록하지 않는다.
 *
 * 받는 메시지: { type: 'config', sampleRate } 들어오는 소리의 샘플레이트, { type: 'push', buffer } 조각, { type: 'clear' } 모두 버리기
 * 보내는 메시지: { type: 'level', value: 0~1 } 약 50ms마다, { type: 'drained' } 쌓아 둔 소리를 다 내보냈을 때 한 번
 */

/** 소리 크기를 알리는 간격(출력 샘플 수). 약 50ms마다 한 번이다. */
const LEVEL_INTERVAL = Math.round(sampleRate / 20);
/** 처음 잡는 버퍼 크기(샘플 수). 24kHz로 10초다. 모자라면 두 배씩 키운다. */
const INITIAL_CAPACITY = 24000 * 10;

class PlayerProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    this.inputRate = 24000;
    /** 출력 샘플 하나마다 입력을 얼마나 나아가는지. 24kHz를 48kHz로 올리면 0.5다. */
    this.ratio = this.inputRate / sampleRate;
    this.buffer = new Int16Array(INITIAL_CAPACITY);
    this.readIndex = 0;
    this.writeIndex = 0;
    /** 아직 내보내지 않은 샘플 수 */
    this.available = 0;
    /** 읽는 위치의 소수 부분. 다음 샘플과 보간한다. */
    this.fraction = 0;
    /** 마지막으로 바닥난 뒤에 소리를 받았는지. 받았다면 다시 바닥날 때 알린다. */
    this.hadData = false;
    this.squares = 0;
    this.counted = 0;

    this.port.onmessage = (event) => this.handle(event.data);
  }

  handle(data) {
    if (!data) return;
    switch (data.type) {
      case 'config':
        if (typeof data.sampleRate === 'number' && data.sampleRate > 0) {
          this.inputRate = data.sampleRate;
          this.ratio = this.inputRate / sampleRate;
        }
        break;
      case 'push':
        if (data.buffer instanceof ArrayBuffer) {
          // 홀수 바이트가 섞여 오면 마지막 반 샘플은 버린다.
          this.append(new Int16Array(data.buffer, 0, Math.floor(data.buffer.byteLength / 2)));
        }
        break;
      case 'clear':
        this.readIndex = 0;
        this.writeIndex = 0;
        this.available = 0;
        this.fraction = 0;
        this.hadData = false;
        break;
      default:
        break;
    }
  }

  append(samples) {
    if (samples.length === 0) return;
    if (this.available + samples.length > this.buffer.length) {
      this.grow(this.available + samples.length);
    }
    const capacity = this.buffer.length;
    const head = Math.min(samples.length, capacity - this.writeIndex);
    this.buffer.set(samples.subarray(0, head), this.writeIndex);
    if (head < samples.length) this.buffer.set(samples.subarray(head), 0);
    this.writeIndex = (this.writeIndex + samples.length) % capacity;
    this.available += samples.length;
    this.hadData = true;
  }

  /** 읽을 차례부터 순서대로 더 큰 버퍼로 옮긴다. */
  grow(needed) {
    let capacity = this.buffer.length;
    while (capacity < needed) capacity *= 2;
    const next = new Int16Array(capacity);
    const tail = Math.min(this.available, this.buffer.length - this.readIndex);
    next.set(this.buffer.subarray(this.readIndex, this.readIndex + tail), 0);
    if (tail < this.available) next.set(this.buffer.subarray(0, this.available - tail), tail);
    this.buffer = next;
    this.readIndex = 0;
    this.writeIndex = this.available;
  }

  process(_inputs, outputs) {
    const output = outputs[0];
    const channel = output && output[0];
    if (!channel) return true;

    const capacity = this.buffer.length;
    for (let i = 0; i < channel.length; i += 1) {
      // 보간하려면 샘플이 둘 필요하다. 하나만 남은 것은 다음 조각이 오면 그 앞에 붙는다.
      if (this.available < 2) {
        channel[i] = 0;
        continue;
      }
      const before = this.buffer[this.readIndex];
      const after = this.buffer[(this.readIndex + 1) % capacity];
      channel[i] = (before + (after - before) * this.fraction) / 32768;
      this.fraction += this.ratio;
      const advance = Math.floor(this.fraction);
      if (advance > 0) {
        const step = Math.min(advance, this.available);
        this.readIndex = (this.readIndex + step) % capacity;
        this.available -= step;
        this.fraction -= advance;
      }
    }

    this.measure(channel);
    if (this.hadData && this.available < 2) {
      this.hadData = false;
      this.port.postMessage({ type: 'drained' });
    }
    return true;
  }

  measure(channel) {
    for (let i = 0; i < channel.length; i += 1) this.squares += channel[i] * channel[i];
    this.counted += channel.length;
    if (this.counted < LEVEL_INTERVAL) return;

    const rms = Math.sqrt(this.squares / this.counted);
    this.port.postMessage({ type: 'level', value: Math.min(1, rms * 3) });
    this.squares = 0;
    this.counted = 0;
  }
}

registerProcessor('naeil-player', PlayerProcessor);
