import { describe, expect, it } from 'vitest';

import {
  aiText,
  audioEnd,
  audioStart,
  finalTranscript,
  modeChanged,
  partialTranscript,
  ready,
  thinking,
  voiceReady,
} from '@/test/fakeSocket';
import {
  conversationReducer,
  initialConversationState,
  type ConversationEvent,
  type ConversationState,
} from '@/talk/conversationState';
import type { ServerMessage } from '@/talk/messages';

const server = (message: unknown): ConversationEvent => ({
  type: 'server',
  message: message as ServerMessage,
});

/** 처음 상태에서 사건을 차례로 적용한다. */
function after(...events: ConversationEvent[]): ConversationState {
  return events.reduce(conversationReducer, initialConversationState);
}

/** 음성을 쓸 수 있는 서버에 연결해 첫 안부까지 받고, 음성이 켜진 상태 */
const listening = (): ConversationEvent[] => [
  server(voiceReady({ mode: 'voice' })),
  server(aiText(0, '오늘 하루는 어떠셨어요?')),
  { type: 'mic', mic: 'on' },
];

describe('대화 상태: 방식', () => {
  it('ready가 실제 방식과 음성 가능 여부를 알려 준다', () => {
    expect(after(server(ready()))).toMatchObject({ mode: 'chat', voiceAvailable: false });
    expect(after(server(voiceReady({ mode: 'voice' })))).toMatchObject({
      mode: 'voice',
      voiceAvailable: true,
    });
  });

  it('mode(voice)면 소리를 올려 보낼 수 있고, mode(chat)면 자막과 소리를 거두고 마이크도 꺼진 것으로 본다', () => {
    const voiced = after(...listening(), server(partialTranscript('오늘')), server(audioStart(0)));
    expect(voiced).toMatchObject({ mode: 'voice', partial: '오늘', speaking: { seq: 0 } });

    const dropped = conversationReducer(voiced, server(modeChanged('chat')));
    expect(dropped).toMatchObject({ mode: 'chat', partial: null, speaking: null, mic: 'off' });
    expect(conversationReducer(dropped, server(modeChanged('voice'))).mode).toBe('voice');
  });

  it('음성을 쓸 수 없다는 오류는 글로 내려 보내고 알린다. 답을 기다리던 글은 그대로 기다린다', () => {
    const waiting = after(...listening(), server(finalTranscript('피곤했어', 'u1')));
    expect(waiting.awaitingReply).toBe(true);

    const state = conversationReducer(
      waiting,
      server({ type: 'error', code: 'voice_unavailable' }),
    );

    expect(state).toMatchObject({
      mode: 'chat',
      mic: 'off',
      notice: 'voice_unavailable',
      awaitingReply: true,
    });
  });

  it('바라지 않았는데 글로 내려오면 마이크를 끄고 알린다', () => {
    expect(after(...listening(), { type: 'voice_dropped' })).toMatchObject({
      mode: 'chat',
      mic: 'off',
      notice: 'voice_unavailable',
    });
  });
});

describe('대화 상태: 알아들은 말', () => {
  it('중간 결과는 자막으로만 두고, 빈 글이면 자막을 비운다', () => {
    const state = after(...listening(), server(partialTranscript('오늘은 좀')));
    expect(state.partial).toBe('오늘은 좀');
    expect(state.messages).toHaveLength(1);
    expect(state.awaitingReply).toBe(false);

    expect(conversationReducer(state, server(partialTranscript(''))).partial).toBeNull();
  });

  it('끝점이 오면 그 말이 보낸 글로 목록에 오르고 답을 기다린다. thinking이 순번을 붙인다', () => {
    const state = after(
      ...listening(),
      server(partialTranscript('오늘은 좀')),
      server(finalTranscript('오늘은 좀 피곤했어', 'u1')),
    );

    expect(state.partial).toBeNull();
    expect(state.awaitingReply).toBe(true);
    expect(state.messages.at(-1)).toEqual({
      key: 'c:u1',
      seq: null,
      speaker: 'user',
      text: '오늘은 좀 피곤했어',
      clientMessageId: 'u1',
      delivery: 'sent',
    });

    const acked = conversationReducer(state, server(thinking('u1', 1)));
    expect(acked.messages.at(-1)).toMatchObject({ seq: 1, delivery: 'sent' });
    expect(acked.awaitingReply).toBe(true);
  });

  it('같은 식별자의 확정본이 두 번 와도 목록에는 한 번만 오른다', () => {
    const state = after(
      ...listening(),
      server(finalTranscript('피곤했어', 'u1')),
      server(finalTranscript('피곤했어', 'u1')),
    );
    expect(state.messages.filter((message) => message.speaker === 'user')).toHaveLength(1);
  });

  it('식별자 없는 확정본은 목록에 올리지 못해도 턴이 도는 것은 안다', () => {
    const state = after(
      ...listening(),
      server(partialTranscript('오늘')),
      server({ type: 'transcript', text: '오늘', final: true }),
    );
    expect(state).toMatchObject({ partial: null, awaitingReply: true });
    expect(state.messages).toHaveLength(1);
  });

  it('thinking이 오면 중간 자막을 거둔다', () => {
    const state = after(
      ...listening(),
      { type: 'submitted', clientMessageId: 't1', text: '글로 쓴 말' },
      server(partialTranscript('말하다 만')),
      server(thinking('t1', 1)),
    );
    expect(state.partial).toBeNull();
  });
});

describe('대화 상태: 소리', () => {
  it('audio_start부터 말하는 중이고, 끝까지 받았어도 다 들려줘야 끝난다', () => {
    const playing = after(...listening(), server(aiText(2, '그랬군요.')), server(audioStart(2)));
    expect(playing).toMatchObject({ speaking: { seq: 2 }, voicedSeq: 2 });

    const received = conversationReducer(playing, server(audioEnd(2, 'done')));
    expect(received.speaking).toEqual({ seq: 2 });

    // 다른 말의 끝은 이 말을 끝내지 않는다.
    expect(conversationReducer(received, { type: 'playback_finished', seq: 1 }).speaking).toEqual({
      seq: 2,
    });
    expect(
      conversationReducer(received, { type: 'playback_finished', seq: 2 }).speaking,
    ).toBeNull();
  });

  it('끊겼거나 만들지 못한 소리는 바로 끝난다', () => {
    const playing = after(...listening(), server(audioStart(2)));
    expect(conversationReducer(playing, server(audioEnd(2, 'interrupted'))).speaking).toBeNull();
    expect(conversationReducer(playing, server(audioEnd(2, 'failed'))).speaking).toBeNull();
    // 다른 순번의 끝은 상관없다.
    expect(conversationReducer(playing, server(audioEnd(1, 'interrupted'))).speaking).toEqual({
      seq: 2,
    });
  });

  it('연결이 끊기면 자막과 소리는 거두되 마이크는 그대로 둔다', () => {
    const state = after(...listening(), server(partialTranscript('오늘')), server(audioStart(0)), {
      type: 'disconnected',
    });
    expect(state).toMatchObject({
      phase: 'reconnecting',
      partial: null,
      speaking: null,
      mic: 'on',
    });
  });

  it('대화가 끝나면 마이크와 소리를 모두 거둔다', () => {
    const state = after(
      ...listening(),
      server(audioStart(0)),
      server({ type: 'ended', reason: 'user', record_date: '2026-09-20', diary_expected: false }),
    );
    expect(state).toMatchObject({ phase: 'ended', speaking: null, mic: 'off' });
  });
});

describe('대화 상태: 마이크', () => {
  it('켜는 중, 켜짐, 꺼짐을 따라간다. 끄면 중간 자막도 거둔다', () => {
    const starting = after(server(voiceReady()), { type: 'mic', mic: 'starting' });
    expect(starting.mic).toBe('starting');

    const on = conversationReducer(starting, { type: 'mic', mic: 'on' });
    expect(on.mic).toBe('on');

    const off = after(...listening(), server(partialTranscript('오늘')), {
      type: 'mic',
      mic: 'off',
    });
    expect(off).toMatchObject({ mic: 'off', partial: null });
  });

  it('켜지 못한 까닭마다 상태와 알림이 다르다', () => {
    expect(after({ type: 'mic_failed', reason: 'denied' })).toMatchObject({
      mic: 'denied',
      notice: 'mic_denied',
    });
    expect(after({ type: 'mic_failed', reason: 'missing' })).toMatchObject({
      mic: 'failed',
      notice: 'mic_missing',
    });
    expect(after({ type: 'mic_failed', reason: 'failed' })).toMatchObject({
      mic: 'failed',
      notice: 'mic_failed',
    });
  });

  it('다시 켜 보면 지난 거절 알림은 거둔다', () => {
    const state = after({ type: 'mic_failed', reason: 'denied' }, { type: 'mic', mic: 'starting' });
    expect(state).toMatchObject({ mic: 'starting', notice: null });
  });

  it('다른 화면이 이어받으면 마이크는 그 화면의 몫이다', () => {
    expect(after(...listening(), { type: 'taken_over' })).toMatchObject({
      phase: 'taken_over',
      mic: 'off',
    });
  });
});
