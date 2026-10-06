import { describe, expect, it, vi } from 'vitest';

import { fakeAudioSystem } from '@/test/fakeAudio';
import { AudioPlayer, PLAYER_PROCESSOR, PLAYER_WORKLET_URL } from '@/talk/audio/player';

describe('재생기', () => {
  it('워클릿을 싣고 목적지에 이은 뒤 들어오는 소리의 샘플레이트를 알린다', async () => {
    const audio = fakeAudioSystem();
    const player = new AudioPlayer(audio);

    await player.start(24_000);

    expect(audio.context().modules).toEqual([PLAYER_WORKLET_URL]);
    const node = audio.latestNode(PLAYER_PROCESSOR);
    expect(node.options).toMatchObject({ numberOfInputs: 0, numberOfOutputs: 1 });
    expect(node.connections).toEqual([audio.context().destination]);
    expect(node.port.posted).toEqual([
      { message: { type: 'config', sampleRate: 24_000 }, transfer: undefined },
    ]);
  });

  it('조각은 복사하지 않고 소유권을 넘긴다. 다 들려주면 알리고, 소리 크기도 전한다', async () => {
    const audio = fakeAudioSystem();
    const player = new AudioPlayer(audio);
    const onDrained = vi.fn();
    const onLevel = vi.fn<(level: number) => void>();
    player.onDrained = onDrained;
    player.onLevel = onLevel;
    await player.start(24_000);
    const port = audio.latestNode(PLAYER_PROCESSOR).port;

    const chunk = new ArrayBuffer(4_800);
    player.push(chunk);

    expect(player.isPlaying()).toBe(true);
    expect(port.posted.at(-1)).toEqual({
      message: { type: 'push', buffer: chunk },
      transfer: [chunk],
    });

    port.emit({ type: 'level', value: 0.3 });
    expect(onLevel).toHaveBeenCalledWith(0.3);

    port.emit({ type: 'drained' });
    expect(player.isPlaying()).toBe(false);
    expect(onDrained).toHaveBeenCalledTimes(1);
  });

  it('준비되기 전에 온 조각은 순서대로 들고 있다가 넘기고, 시작 전에 온 조각은 버린다', async () => {
    const audio = fakeAudioSystem();
    const player = new AudioPlayer(audio);
    player.push(new ArrayBuffer(2));

    const starting = player.start(24_000);
    const first = new ArrayBuffer(4);
    const second = new ArrayBuffer(6);
    player.push(first);
    player.push(second);
    expect(player.isPlaying()).toBe(true);
    await starting;

    const pushed = audio.latestNode(PLAYER_PROCESSOR).port.postedOfType('push');
    expect(pushed).toEqual([
      { type: 'push', buffer: first },
      { type: 'push', buffer: second },
    ]);
  });

  it('비우면 워클릿에도 알리고 더 들려줄 것이 없는 상태가 된다', async () => {
    const audio = fakeAudioSystem();
    const player = new AudioPlayer(audio);
    await player.start(24_000);
    player.push(new ArrayBuffer(4));

    player.clear();

    expect(player.isPlaying()).toBe(false);
    expect(audio.latestNode(PLAYER_PROCESSOR).port.postedOfType('clear')).toEqual([
      { type: 'clear' },
    ]);
  });

  it('다시 시작하면 노드를 새로 만들지 않고 샘플레이트만 다시 알린다', async () => {
    const audio = fakeAudioSystem();
    const player = new AudioPlayer(audio);
    await player.start(24_000);

    await player.start(16_000);

    expect(audio.nodes).toHaveLength(1);
    expect(audio.latestNode(PLAYER_PROCESSOR).port.postedOfType('config')).toEqual([
      { type: 'config', sampleRate: 24_000 },
      { type: 'config', sampleRate: 16_000 },
    ]);
  });

  it('멈추면 노드를 끊고, 그 뒤의 메시지는 전하지 않는다', async () => {
    const audio = fakeAudioSystem();
    const player = new AudioPlayer(audio);
    const onDrained = vi.fn();
    player.onDrained = onDrained;
    await player.start(24_000);
    const node = audio.latestNode(PLAYER_PROCESSOR);

    player.stop();

    expect(node.disconnected).toBe(1);
    node.port.emit({ type: 'drained' });
    expect(onDrained).not.toHaveBeenCalled();
    // 다시 시작하면 새 노드를 만든다.
    await player.start(24_000);
    expect(audio.nodes).toHaveLength(2);
  });

  it('워클릿을 싣지 못하면 시작이 실패하고 들고 있던 조각은 버린다', async () => {
    const audio = fakeAudioSystem();
    audio.moduleError = new Error('404');
    const player = new AudioPlayer(audio);

    const starting = player.start(24_000);
    player.push(new ArrayBuffer(4));
    await expect(starting).rejects.toThrow('404');

    expect(player.isPlaying()).toBe(false);
    expect(audio.nodes).toHaveLength(0);
  });
});
