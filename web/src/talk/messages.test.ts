import { describe, expect, it } from 'vitest';

import {
  DEFAULT_PLAYBACK_SAMPLE_RATE,
  encodeClientMessage,
  parseServerMessage,
} from '@/talk/messages';

const parse = (value: unknown) => parseServerMessage(JSON.stringify(value));

describe('서버 메시지 읽기: 음성', () => {
  it('ready에 실제 방식과 음성 가능 여부가 실려 온다. 옛 서버가 보내지 않으면 글 방식으로 읽는다', () => {
    const base = { type: 'ready', conversation_id: 'c', record_date: '2026-09-20' };

    expect(parse({ ...base, mode: 'voice', voice_available: true })).toMatchObject({
      mode: 'voice',
      voice_available: true,
    });
    expect(parse(base)).toMatchObject({ mode: 'chat', voice_available: false });
    expect(parse({ ...base, mode: 'video', voice_available: 'yes' })).toMatchObject({
      mode: 'chat',
      voice_available: false,
    });
  });

  it('mode를 읽는다. 모르는 방식은 소리를 보낼 수 없으니 글로 읽는다', () => {
    expect(parse({ type: 'mode', mode: 'voice' })).toEqual({ type: 'mode', mode: 'voice' });
    expect(parse({ type: 'mode', mode: 'chat' })).toEqual({ type: 'mode', mode: 'chat' });
    expect(parse({ type: 'mode', mode: 'video' })).toEqual({ type: 'mode', mode: 'chat' });
  });

  it('transcript의 중간 결과와 확정본을 읽는다', () => {
    expect(parse({ type: 'transcript', text: '오늘은', final: false })).toEqual({
      type: 'transcript',
      text: '오늘은',
      final: false,
    });
    expect(
      parse({
        type: 'transcript',
        text: '오늘은 좀 피곤했어',
        final: true,
        client_message_id: 'u1',
      }),
    ).toEqual({
      type: 'transcript',
      text: '오늘은 좀 피곤했어',
      final: true,
      client_message_id: 'u1',
    });
    // final이 빠져 있으면 중간 결과로 읽는다. 글이 없으면 읽을 수 없다.
    expect(parse({ type: 'transcript', text: '…' })).toMatchObject({ final: false });
    expect(parse({ type: 'transcript', final: true })).toBeNull();
  });

  it('audio_start와 audio_end를 읽는다. 샘플레이트가 이상하면 기본값, 모르는 까닭은 끝까지 들려주는 쪽이다', () => {
    expect(parse({ type: 'audio_start', seq: 3, sample_rate: 24000 })).toEqual({
      type: 'audio_start',
      seq: 3,
      sample_rate: 24000,
    });
    expect(parse({ type: 'audio_start', seq: 3, sample_rate: 'fast' })).toEqual({
      type: 'audio_start',
      seq: 3,
      sample_rate: DEFAULT_PLAYBACK_SAMPLE_RATE,
    });
    expect(parse({ type: 'audio_start', seq: -1, sample_rate: 24000 })).toBeNull();

    expect(parse({ type: 'audio_end', seq: 3, reason: 'interrupted' })).toEqual({
      type: 'audio_end',
      seq: 3,
      reason: 'interrupted',
    });
    expect(parse({ type: 'audio_end', seq: 3, reason: 'paused' })).toEqual({
      type: 'audio_end',
      seq: 3,
      reason: 'done',
    });
  });

  it('음성을 쓸 수 없다는 오류 코드를 안다', () => {
    expect(parse({ type: 'error', code: 'voice_unavailable' })).toEqual({
      type: 'error',
      code: 'voice_unavailable',
    });
  });

  it('모르는 종류와 바이너리는 조용히 버린다', () => {
    expect(parse({ type: 'audio_level', value: 0.4 })).toBeNull();
    expect(parseServerMessage(new ArrayBuffer(8))).toBeNull();
  });
});

describe('클라이언트 메시지 쓰기: 음성', () => {
  it('방식 바꾸기, 끼어들기, 다 말했어요를 JSON으로 만든다', () => {
    expect(JSON.parse(encodeClientMessage({ type: 'start', mode: 'voice' }))).toEqual({
      type: 'start',
      mode: 'voice',
    });
    expect(JSON.parse(encodeClientMessage({ type: 'set_mode', mode: 'chat' }))).toEqual({
      type: 'set_mode',
      mode: 'chat',
    });
    expect(JSON.parse(encodeClientMessage({ type: 'interrupt' }))).toEqual({ type: 'interrupt' });
    expect(JSON.parse(encodeClientMessage({ type: 'finalize' }))).toEqual({ type: 'finalize' });
  });
});
