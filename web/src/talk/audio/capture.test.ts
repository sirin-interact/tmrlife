import { describe, expect, it, vi } from 'vitest';

import { fakeAudioSystem, fakeStream, notAllowedError, notFoundError } from '@/test/fakeAudio';
import {
  CAPTURE_FRAME_BYTES,
  CAPTURE_PROCESSOR,
  CAPTURE_WORKLET_URL,
  MicCapture,
  MicError,
  micFailureOf,
} from '@/talk/audio/capture';

function callbacks() {
  return {
    onFrame: vi.fn<(frame: ArrayBuffer) => void>(),
    onLevel: vi.fn<(level: number) => void>(),
  };
}

describe('마이크 캡처', () => {
  it('워클릿을 싣고, 에코 제거를 켠 모노 마이크를 열어, 마이크 → 워클릿 → 목적지로 잇는다', async () => {
    const audio = fakeAudioSystem();
    const capture = new MicCapture(audio);

    await capture.start(callbacks());

    expect(audio.context().modules).toEqual([CAPTURE_WORKLET_URL]);
    expect(audio.constraints).toEqual([
      {
        audio: {
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
          channelCount: 1,
        },
      },
    ]);
    const node = audio.latestNode(CAPTURE_PROCESSOR);
    expect(node.options).toMatchObject({ numberOfInputs: 1, numberOfOutputs: 1 });
    expect(audio.context().sources[0]?.connections).toEqual([node]);
    // 워클릿은 무음을 내보낸다. 목적지까지 이어 두어야 브라우저가 이 노드를 돌린다.
    expect(node.connections).toEqual([audio.context().destination]);
  });

  it('워클릿이 넘긴 100ms 조각과 소리 크기를 그대로 전한다', async () => {
    const audio = fakeAudioSystem();
    const capture = new MicCapture(audio);
    const received = callbacks();
    await capture.start(received);
    const port = audio.latestNode(CAPTURE_PROCESSOR).port;

    const frame = new ArrayBuffer(CAPTURE_FRAME_BYTES);
    port.emit({ type: 'frame', buffer: frame });
    port.emit({ type: 'level', value: 0.4 });
    port.emit({ type: 'something_else' });
    port.emit('not an object');

    expect(received.onFrame).toHaveBeenCalledTimes(1);
    expect(received.onFrame.mock.calls[0]?.[0]).toBe(frame);
    expect(frame.byteLength).toBe(3200);
    expect(received.onLevel).toHaveBeenCalledWith(0.4);
  });

  it('멈추면 트랙을 놓고 노드를 끊는다. 그 뒤의 메시지는 전하지 않는다', async () => {
    const audio = fakeAudioSystem();
    const capture = new MicCapture(audio);
    const received = callbacks();
    await capture.start(received);
    const node = audio.latestNode(CAPTURE_PROCESSOR);

    capture.stop();

    expect(audio.streams[0]?.tracks[0]?.stopped).toBe(true);
    expect(node.disconnected).toBe(1);
    expect(audio.context().sources[0]?.disconnected).toBe(1);
    node.port.emit({ type: 'level', value: 0.4 });
    expect(received.onLevel).not.toHaveBeenCalled();
  });

  it('권한이 거절되면 denied, 마이크가 없으면 missing, 그 밖에는 failed로 알린다', async () => {
    const audio = fakeAudioSystem();
    const capture = new MicCapture(audio);

    audio.denyWith = notAllowedError();
    await expect(capture.start(callbacks())).rejects.toMatchObject({ reason: 'denied' });

    audio.denyWith = notFoundError();
    await expect(capture.start(callbacks())).rejects.toMatchObject({ reason: 'missing' });

    audio.denyWith = new Error('boom');
    await expect(capture.start(callbacks())).rejects.toMatchObject({ reason: 'failed' });
    expect(audio.nodes).toHaveLength(0);
  });

  it('안전하지 않은 주소면 워클릿도 권한도 건드리지 않고 insecure로 알린다', async () => {
    const audio = fakeAudioSystem();
    audio.secure = false;
    const capture = new MicCapture(audio);

    await expect(capture.start(callbacks())).rejects.toMatchObject({ reason: 'insecure' });
    expect(audio.constraints).toHaveLength(0);
    expect(audio.nodes).toHaveLength(0);
  });

  it('워클릿을 싣지 못하면 마이크 권한을 묻지 않고 failed로 알린다', async () => {
    const audio = fakeAudioSystem();
    audio.moduleError = new Error('404');
    const capture = new MicCapture(audio);

    await expect(capture.start(callbacks())).rejects.toBeInstanceOf(MicError);
    expect(audio.constraints).toHaveLength(0);
  });

  it('권한을 기다리는 사이에 멈추면 받은 트랙을 바로 놓고 노드를 만들지 않는다', async () => {
    const audio = fakeAudioSystem();
    const stream = fakeStream();
    let grant: ((stream: MediaStream) => void) | null = null;
    audio.getUserMedia = () =>
      new Promise((resolve) => {
        grant = resolve;
      });
    const capture = new MicCapture(audio);

    const starting = capture.start(callbacks());
    await Promise.resolve();
    capture.stop();
    grant!(stream);
    await starting;

    expect(stream.tracks[0]?.stopped).toBe(true);
    expect(audio.nodes).toHaveLength(0);
  });

  it('브라우저 오류의 이름으로 까닭을 가른다', () => {
    expect(micFailureOf(notAllowedError())).toBe('denied');
    expect(micFailureOf(new DOMException('', 'SecurityError'))).toBe('denied');
    expect(micFailureOf(notFoundError())).toBe('missing');
    expect(micFailureOf(new DOMException('', 'OverconstrainedError'))).toBe('missing');
    expect(micFailureOf(new DOMException('', 'NotReadableError'))).toBe('failed');
    expect(micFailureOf(undefined)).toBe('failed');
    expect(micFailureOf(new MicError('denied'))).toBe('denied');
  });
});
