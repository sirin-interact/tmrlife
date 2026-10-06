import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { FakePort } from '@/test/fakeAudio';
import { CAPTURE_FRAME_BYTES, CAPTURE_PROCESSOR } from '@/talk/audio/capture';
import { PLAYER_PROCESSOR } from '@/talk/audio/player';

/**
 * 워클릿 파일은 오디오 스레드의 전역(sampleRate, AudioWorkletProcessor, registerProcessor)을 쓴다.
 * 그 전역을 흉내 내고 파일을 그대로 불러들여, 등록된 프로세서를 직접 돌려 본다.
 */

interface Processor {
  port: FakePort;
  process(inputs: Float32Array[][], outputs: Float32Array[][]): boolean;
}

type ProcessorClass = new () => Processor;

const CONTEXT_RATE = 48_000;
const registry = new Map<string, ProcessorClass>();

async function load(file: string): Promise<ProcessorClass> {
  vi.stubGlobal('sampleRate', CONTEXT_RATE);
  vi.stubGlobal(
    'AudioWorkletProcessor',
    class {
      port = new FakePort();
    },
  );
  vi.stubGlobal('registerProcessor', (name: string, processor: ProcessorClass) => {
    registry.set(name, processor);
  });
  const path = new URL(`../../../public/audio/${file}`, import.meta.url).pathname;
  await import(/* @vite-ignore */ path);
  const processor = registry.get(
    file === 'capture-worklet.js' ? CAPTURE_PROCESSOR : PLAYER_PROCESSOR,
  );
  if (!processor) throw new Error(`${file}이 프로세서를 등록하지 않았다`);
  return processor;
}

beforeEach(() => {
  vi.resetModules();
});

afterEach(() => {
  registry.clear();
});

/** 같은 값으로 채운 128샘플 블록 */
function block(value: number, length = 128): Float32Array[][] {
  return [[new Float32Array(length).fill(value)]];
}

describe('캡처 워클릿', () => {
  it('48kHz 입력을 16kHz로 내려 100ms(3,200바이트) 조각으로 넘기고, 소유권을 넘긴다', async () => {
    const Capture = await load('capture-worklet.js');
    const processor = new Capture();

    // 16kHz의 1,600샘플은 48kHz의 4,800샘플이다. 128샘플 블록 38개면 넘친다.
    for (let i = 0; i < 38; i += 1) processor.process(block(0.5), []);

    const frames = processor.port.posted.filter(
      (item) => (item.message as { type: string }).type === 'frame',
    );
    expect(frames).toHaveLength(1);
    const buffer = (frames[0]!.message as { buffer: ArrayBuffer }).buffer;
    expect(buffer.byteLength).toBe(CAPTURE_FRAME_BYTES);
    expect(frames[0]!.transfer).toEqual([buffer]);
    const samples = new Int16Array(buffer);
    expect(samples).toHaveLength(1600);
    // 0.5는 16비트로 16384다. 같은 값만 넣었으니 보간해도 같다.
    expect(samples.every((sample) => sample === 16384)).toBe(true);
  });

  it('값이 변하는 소리는 사이를 보간한다. 1을 넘는 값은 잘라 낸다', async () => {
    const Capture = await load('capture-worklet.js');
    const processor = new Capture();
    // 0에서 1까지 올라가는 경사. 48kHz에서 3샘플마다 하나를 뽑으므로 값도 3칸씩 뛴다.
    const ramp = new Float32Array(4_800).map((_, i) => (i / 4_800) * 2);
    for (let start = 0; start < 4_800; start += 128) {
      processor.process([[ramp.subarray(start, start + 128)]], []);
    }

    const frame = processor.port.posted.find(
      (item) => (item.message as { type: string }).type === 'frame',
    );
    const samples = new Int16Array((frame!.message as { buffer: ArrayBuffer }).buffer);
    expect(samples[0]).toBe(0);
    expect(samples[1]).toBe(Math.round((3 / 4_800) * 2 * 32767));
    expect(samples[1599]).toBe(32767);
  });

  it('소리 크기를 50ms마다 알리고, 조용하면 0이다', async () => {
    const Capture = await load('capture-worklet.js');
    const processor = new Capture();

    for (let i = 0; i < 19; i += 1) processor.process(block(0.2), []);
    for (let i = 0; i < 19; i += 1) processor.process(block(0), []);

    const levels = processor.port.postedOfType('level') as Array<{ value: number }>;
    expect(levels).toHaveLength(2);
    expect(levels[0]!.value).toBeCloseTo(0.6, 5);
    expect(levels[1]!.value).toBe(0);
  });

  it('입력을 꺼 두라고 하면 아무것도 보내지 않는다', async () => {
    const Capture = await load('capture-worklet.js');
    const processor = new Capture();
    processor.port.emit({ type: 'mute', value: true });

    for (let i = 0; i < 40; i += 1) processor.process(block(0.5), []);
    expect(processor.port.posted).toHaveLength(0);

    processor.port.emit({ type: 'mute', value: false });
    for (let i = 0; i < 40; i += 1) processor.process(block(0.5), []);
    expect(processor.port.postedOfType('frame')).toHaveLength(1);
  });

  it('입력이 없는 블록에도 살아 있다', async () => {
    const Capture = await load('capture-worklet.js');
    const processor = new Capture();
    expect(processor.process([[]], [])).toBe(true);
    expect(processor.process([], [])).toBe(true);
  });
});

describe('재생 워클릿', () => {
  function pcm(...values: number[]): ArrayBuffer {
    return new Int16Array(values).buffer;
  }

  /** 같은 값으로 채운 긴 조각 */
  function filled(length: number, value: number): ArrayBuffer {
    return new Int16Array(length).fill(value).buffer;
  }

  it('24kHz 조각을 48kHz로 올려 재생하고, 다 내보내면 한 번 알린다', async () => {
    const Player = await load('player-worklet.js');
    const processor = new Player();
    processor.port.emit({ type: 'config', sampleRate: 24_000 });
    processor.port.emit({ type: 'push', buffer: pcm(0, 16384, 32767, 0) });

    const output = new Float32Array(16);
    processor.process([], [[output]]);

    expect(Array.from(output.subarray(0, 7)).map((v) => Number(v.toFixed(3)))).toEqual([
      0, 0.25, 0.5, 0.75, 1, 0.5, 0,
    ]);
    expect(processor.port.postedOfType('drained')).toEqual([{ type: 'drained' }]);

    // 비어 있는 동안에는 무음이고, 다시 알리지 않는다.
    processor.process([], [[output]]);
    expect(output.every((v) => v === 0)).toBe(true);
    expect(processor.port.postedOfType('drained')).toHaveLength(1);
  });

  it('조각이 블록보다 작아도 이어 붙여 끊김 없이 낸다', async () => {
    const Player = await load('player-worklet.js');
    const processor = new Player();
    processor.port.emit({ type: 'config', sampleRate: 24_000 });
    // 24kHz 샘플 하나는 48kHz 출력 둘이다. 1,000샘플을 100샘플씩 열 번 넣으면 2,000샘플이 나와야 한다.
    for (let i = 0; i < 10; i += 1) {
      processor.port.emit({ type: 'push', buffer: filled(100, 8192) });
    }

    let produced = 0;
    for (let i = 0; i < 20; i += 1) {
      const output = new Float32Array(128);
      processor.process([], [[output]]);
      produced += output.filter((v) => v > 0).length;
    }
    // 마지막 샘플은 다음 조각과 보간하려고 남겨 둔다. 둘 빼고 모두 나온다.
    expect(produced).toBe(2_000 - 2);
  });

  it('비우면 남은 소리를 내지 않고, 바닥났다고도 알리지 않는다', async () => {
    const Player = await load('player-worklet.js');
    const processor = new Player();
    processor.port.emit({ type: 'push', buffer: filled(400, 8192) });
    processor.port.emit({ type: 'clear' });

    const output = new Float32Array(128);
    processor.process([], [[output]]);

    expect(output.every((v) => v === 0)).toBe(true);
    expect(processor.port.postedOfType('drained')).toHaveLength(0);
  });

  it('버퍼보다 긴 소리가 오면 버퍼를 키운다', async () => {
    const Player = await load('player-worklet.js');
    const processor = new Player();
    // 처음 버퍼는 24kHz로 10초다. 11초를 넣는다.
    processor.port.emit({ type: 'push', buffer: filled(24_000 * 11, 1024) });

    const output = new Float32Array(128);
    processor.process([], [[output]]);

    expect(output[0]).toBeCloseTo(1024 / 32768, 6);
    expect(output[127]).toBeCloseTo(1024 / 32768, 6);
  });

  it('소리 크기를 50ms마다 알린다', async () => {
    const Player = await load('player-worklet.js');
    const processor = new Player();
    processor.port.emit({ type: 'push', buffer: filled(4_800, 16384) });

    for (let i = 0; i < 19; i += 1) processor.process([], [[new Float32Array(128)]]);

    const levels = processor.port.postedOfType('level') as Array<{ value: number }>;
    expect(levels).toHaveLength(1);
    expect(levels[0]!.value).toBeCloseTo(1, 5);
  });
});
