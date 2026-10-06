import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { FakeCapture, FakePlayer, notAllowedError, notFoundError } from '@/test/fakeAudio';
import {
  aiText,
  audioEnd,
  audioStart,
  CONVERSATION_ID,
  fakeSockets,
  finalTranscript,
  listening as serverListening,
  modeChanged,
  partialTranscript,
  ready,
  RECORD_DATE,
  testResources,
  thinking,
  utterance,
  voiceReady,
  type FakeSockets,
} from '@/test/fakeSocket';
import {
  ConversationClient,
  MAX_RECONNECT_ATTEMPTS,
  MAX_SEND_ATTEMPTS,
  TIMING,
} from '@/talk/conversationClient';
import { CLOSE_CODE, CONVERSATION_PATH, conversationUrl } from '@/talk/messages';

const URL = 'ws://naeil.test/ws/v1/conversation';

interface Harness {
  client: ConversationClient;
  sockets: FakeSockets;
  ids: string[];
  online: { value: boolean };
  handshakeFailed: ReturnType<typeof vi.fn<() => void>>;
  capture: FakeCapture;
  player: FakePlayer;
}

function setup(): Harness {
  const sockets = fakeSockets();
  const ids: string[] = [];
  const online = { value: true };
  const handshakeFailed = vi.fn<() => void>();
  const capture = new FakeCapture();
  const player = new FakePlayer();
  let counter = 0;
  const client = new ConversationClient({
    url: URL,
    createSocket: sockets.create,
    createCapture: () => capture,
    createPlayer: () => player,
    newId: () => {
      counter += 1;
      const id = `00000000-0000-4000-8000-${String(counter).padStart(12, '0')}`;
      ids.push(id);
      return id;
    },
    // 기다림을 가장 길게 잡는다. 테스트가 "이만큼 지나면 반드시 다시 연결한다"를 말할 수 있다.
    random: () => 0.999999,
    isOnline: () => online.value,
    onHandshakeFailed: handshakeFailed,
  });
  return { client, sockets, ids, online, handshakeFailed, capture, player };
}

/** 연결해서 새 대화의 ready와 첫 안부까지 받은 상태 */
function started(readyMessage = ready()): Harness {
  const harness = setup();
  harness.client.start();
  const socket = harness.sockets.latest();
  socket.open();
  socket.receive(readyMessage);
  socket.receive(aiText(0, '오늘 하루는 어떠셨어요?'));
  return harness;
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
  localStorage.clear();
});

describe('대화 채널의 주소', () => {
  it('화면과 같은 출처로, https면 wss로 연결한다', () => {
    expect(conversationUrl({ protocol: 'https:', host: 'naeil.example' })).toBe(
      `wss://naeil.example${CONVERSATION_PATH}`,
    );
    expect(conversationUrl({ protocol: 'http:', host: 'localhost:5173' })).toBe(
      `ws://localhost:5173${CONVERSATION_PATH}`,
    );
  });
});

describe('연결과 시작', () => {
  it('연결이 열리면 채팅 방식으로 start를 보내고, ready를 받아야 글을 보낼 수 있다', () => {
    const { client, sockets } = setup();

    client.start();
    expect(client.getState().phase).toBe('connecting');
    expect(client.send('아직 연결 전이에요')).toBe(false);

    const socket = sockets.latest();
    expect(socket.url).toBe(URL);
    socket.open();
    expect(socket.sent).toEqual([{ type: 'start', mode: 'chat' }]);
    expect(client.getState().phase).toBe('connecting');

    socket.receive(ready());
    expect(client.getState()).toMatchObject({
      phase: 'ready',
      conversationId: CONVERSATION_ID,
      recordDate: RECORD_DATE,
    });
  });

  it('첫 안부는 ready 뒤에 오는 ai_text로 받고, 화면 낭독기에 읽어 줄 말로 표시한다', () => {
    const { client } = started();

    expect(client.getState().messages).toEqual([
      expect.objectContaining({ speaker: 'ai', seq: 0, text: '오늘 하루는 어떠셨어요?' }),
    ]);
    expect(client.getState().arrived).toEqual({ seq: 0, text: '오늘 하루는 어떠셨어요?' });
  });

  it('읽을 수 없는 프레임과 모르는 종류의 메시지는 조용히 버린다', () => {
    const { client, sockets } = started();
    const before = client.getState();

    sockets.latest().receiveRaw('not json');
    sockets.latest().receiveRaw(new ArrayBuffer(4));
    sockets.latest().receive({ type: 'stage_changed', stage: 2 });
    sockets.latest().receive({ type: 'ai_text', seq: -1, text: 'bad seq' });

    expect(client.getState()).toBe(before);
  });

  it('ready가 제때 오지 않으면 연결을 버리고 다시 잇는다', () => {
    const { client, sockets } = setup();
    client.start();
    sockets.latest().open();

    vi.advanceTimersByTime(TIMING.connectTimeoutMs);

    expect(sockets.latest().closedByClient).toBe(true);
    expect(client.getState().phase).toBe('reconnecting');
    vi.advanceTimersByTime(TIMING.reconnectBaseMs);
    expect(sockets.all).toHaveLength(2);
  });
});

describe('글 보내기', () => {
  it('글마다 새 식별자를 붙여 보내고, thinking을 받으면 순번과 함께 보낸 것으로 표시한다', () => {
    const { client, sockets, ids } = started();

    expect(client.send('  오늘 친구랑 놀러갔다왔어  ')).toBe(true);

    expect(sockets.latest().sentOfType('user_text')).toEqual([
      { type: 'user_text', client_message_id: ids[0], text: '오늘 친구랑 놀러갔다왔어' },
    ]);
    expect(client.getState().awaitingReply).toBe(true);
    expect(client.getState().messages.at(-1)).toMatchObject({ delivery: 'sending', seq: null });

    sockets.latest().receive(thinking(ids[0]!, 1));
    expect(client.getState().messages.at(-1)).toMatchObject({ delivery: 'sent', seq: 1 });
    expect(client.getState().awaitingReply).toBe(true);

    sockets.latest().receive(aiText(2, '오 재밌게 놀고 오셨어요?'));
    expect(client.getState().awaitingReply).toBe(false);
    expect(client.getState().messages.map((message) => message.seq)).toEqual([0, 1, 2]);
  });

  it('빈 글과 너무 긴 글은 보내지 않는다', () => {
    const { client, sockets } = started();

    expect(client.send('   \n ')).toBe(false);
    expect(client.send('가'.repeat(2001))).toBe(false);
    expect(client.send('가'.repeat(2000))).toBe(true);

    expect(sockets.latest().sentOfType('user_text')).toHaveLength(1);
  });

  it('답을 기다리는 동안에는 다음 글을 받지 않는다', () => {
    const { client, sockets } = started();
    client.send('첫 번째');

    expect(client.send('두 번째')).toBe(false);

    expect(sockets.latest().sentOfType('user_text')).toHaveLength(1);
  });

  it('thinking이 오지 않으면 연결을 새로 잇고 같은 식별자로 다시 보낸다', () => {
    const { client, sockets, ids } = started();
    client.send('들리나요');

    vi.advanceTimersByTime(TIMING.ackTimeoutMs);
    expect(sockets.all[0]?.closedByClient).toBe(true);
    expect(client.getState().phase).toBe('reconnecting');

    vi.advanceTimersByTime(TIMING.reconnectBaseMs);
    const second = sockets.latest();
    second.open();
    // 서버는 그 글을 받지 못했다. 지난 발화에 없다.
    second.receive(
      ready({ resumed: true, utterances: [utterance(0, 'ai', '오늘 하루는 어떠셨어요?')] }),
    );

    expect(second.sentOfType('user_text')).toEqual([
      { type: 'user_text', client_message_id: ids[0], text: '들리나요' },
    ]);
    expect(client.getState().messages.map((message) => message.text)).toEqual([
      '오늘 하루는 어떠셨어요?',
      '들리나요',
    ]);
  });

  it('서버에 저장됐지만 답을 받지 못한 글도, 다시 이어지면 같은 식별자로 보내 답을 받는다', () => {
    const { client, sockets, ids } = started();
    client.send('답이 오기 전에 끊겼어');
    sockets.latest().receive(thinking(ids[0]!, 1));

    sockets.latest().drop();
    vi.advanceTimersByTime(TIMING.reconnectBaseMs);
    const second = sockets.latest();
    second.open();
    second.receive(
      ready({
        resumed: true,
        utterances: [
          utterance(0, 'ai', '오늘 하루는 어떠셨어요?'),
          utterance(1, 'user', '답이 오기 전에 끊겼어', ids[0]),
        ],
      }),
    );

    expect(second.sentOfType('user_text')).toEqual([
      { type: 'user_text', client_message_id: ids[0], text: '답이 오기 전에 끊겼어' },
    ]);
    // 같은 글이 화면에 두 번 보이지 않는다.
    expect(client.getState().messages).toHaveLength(2);
    expect(client.getState().awaitingReply).toBe(true);
  });

  it(`같은 글을 ${MAX_SEND_ATTEMPTS}번 보내도 확인이 없으면 그만두고, 사용자가 다시 보낼 수 있게 한다`, () => {
    const { client, sockets, ids } = started();
    client.send('계속 안 가는 글');

    for (let attempt = 1; attempt < MAX_SEND_ATTEMPTS; attempt += 1) {
      vi.advanceTimersByTime(TIMING.ackTimeoutMs);
      vi.advanceTimersByTime(TIMING.reconnectBaseMs);
      sockets.latest().open();
      sockets.latest().receive(ready({ resumed: true }));
    }
    vi.advanceTimersByTime(TIMING.ackTimeoutMs);
    vi.advanceTimersByTime(TIMING.reconnectBaseMs);
    sockets.latest().open();
    sockets.latest().receive(ready({ resumed: true }));

    expect(client.getState().messages.at(-1)).toMatchObject({ delivery: 'failed' });
    expect(client.getState()).toMatchObject({ awaitingReply: false, notice: 'send_failed' });
    const sentBefore = sockets.all.flatMap((socket) => socket.sentOfType('user_text'));
    expect(sentBefore).toHaveLength(MAX_SEND_ATTEMPTS);

    client.retry(ids[0]!);

    expect(sockets.latest().sentOfType('user_text').at(-1)).toEqual({
      type: 'user_text',
      client_message_id: ids[0],
      text: '계속 안 가는 글',
    });
    expect(client.getState().messages.at(-1)).toMatchObject({ delivery: 'sending' });
  });

  it('서버가 그 글을 처리하지 못했다고 알리면 실패로 표시한다', () => {
    const { client, sockets, ids } = started();
    client.send('처리하다 실패한 글');

    sockets.latest().receive({ type: 'error', code: 'internal_error', client_message_id: ids[0] });

    expect(client.getState().messages.at(-1)).toMatchObject({ delivery: 'failed' });
    expect(client.getState()).toMatchObject({ awaitingReply: false, notice: 'send_failed' });
    // 받았다는 확인을 기다리던 타이머가 남아 연결을 끊지 않는다.
    vi.advanceTimersByTime(TIMING.ackTimeoutMs * 2);
    expect(sockets.all).toHaveLength(1);
  });

  it('너무 자주 보냈다는 답을 받으면 잠시 뒤에 같은 식별자로 다시 보낸다', () => {
    const { client, sockets, ids } = started();
    client.send('조금 빨랐던 글');

    sockets.latest().receive({ type: 'error', code: 'rate_limited', client_message_id: ids[0] });
    expect(client.getState().notice).toBe('slow_down');
    expect(client.getState().messages.at(-1)).toMatchObject({ delivery: 'sending' });

    vi.advanceTimersByTime(TIMING.rateLimitRetryMs);

    expect(sockets.latest().sentOfType('user_text')).toEqual([
      { type: 'user_text', client_message_id: ids[0], text: '조금 빨랐던 글' },
      { type: 'user_text', client_message_id: ids[0], text: '조금 빨랐던 글' },
    ]);
    sockets.latest().receive(thinking(ids[0]!, 1));
    expect(client.getState().notice).toBeNull();
  });

  it('thinking 뒤에 답이 너무 오래 없으면 연결이 죽은 것으로 보고 다시 잇는다', () => {
    const { client, sockets, ids } = started();
    client.send('답이 안 와요');
    sockets.latest().receive(thinking(ids[0]!, 1));

    vi.advanceTimersByTime(TIMING.replyTimeoutMs);

    expect(sockets.all[0]?.closedByClient).toBe(true);
    expect(client.getState().phase).toBe('reconnecting');
  });
});

describe('끊긴 연결', () => {
  it('간격을 늘려 가며 다시 잇고, 이어지면 서버가 준 지난 발화로 화면을 맞춘다', () => {
    const { client, sockets } = started();

    sockets.latest().drop();
    expect(client.getState().phase).toBe('reconnecting');
    // 지난 대화는 끊긴 동안에도 그대로 보인다.
    expect(client.getState().messages).toHaveLength(1);

    vi.advanceTimersByTime(TIMING.reconnectBaseMs - 1);
    expect(sockets.all).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(sockets.all).toHaveLength(2);

    // 두 번째 시도도 실패하면 기다림이 두 배가 된다.
    sockets.latest().drop();
    vi.advanceTimersByTime(TIMING.reconnectBaseMs * 2 - 1);
    expect(sockets.all).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(sockets.all).toHaveLength(3);

    const third = sockets.latest();
    third.open();
    expect(third.sent).toEqual([{ type: 'start', mode: 'chat' }]);
    third.receive(
      ready({
        resumed: true,
        utterances: [
          utterance(0, 'ai', '오늘 하루는 어떠셨어요?'),
          utterance(1, 'user', '다른 기기에서 쓴 글', '11111111-1111-4111-8111-111111111111'),
          utterance(2, 'ai', '그랬군요.'),
        ],
      }),
    );

    expect(client.getState().phase).toBe('ready');
    expect(client.getState().messages.map((message) => message.text)).toEqual([
      '오늘 하루는 어떠셨어요?',
      '다른 기기에서 쓴 글',
      '그랬군요.',
    ]);
    // 이어받은 지난 발화는 방금 도착한 말이 아니다. 화면 낭독기에 다시 읽어 주지 않는다.
    expect(client.getState().arrived).toBeNull();
    expect(client.getState().awaitingReply).toBe(false);
  });

  it('기다림은 실패할 때마다 두 배가 되고, 정해 둔 한도를 넘지 않는다', () => {
    const { sockets } = started();

    for (let failure = 1; failure < MAX_RECONNECT_ATTEMPTS; failure += 1) {
      const wait = Math.min(TIMING.reconnectMaxMs, TIMING.reconnectBaseMs * 2 ** (failure - 1));
      sockets.latest().drop();

      vi.advanceTimersByTime(wait - 1);
      expect(sockets.all, `${failure}번째 실패 뒤, 기다림이 끝나기 전`).toHaveLength(failure);
      vi.advanceTimersByTime(1);
      expect(sockets.all, `${failure}번째 실패 뒤`).toHaveLength(failure + 1);
    }
  });

  it(`이어서 ${MAX_RECONNECT_ATTEMPTS}번 실패하면 그만두고, 사용자가 다시 시도하면 처음부터 다시 잇는다`, () => {
    const { client, sockets } = started();

    for (let failure = 1; failure <= MAX_RECONNECT_ATTEMPTS; failure += 1) {
      const before = sockets.all.length;
      sockets.latest().drop();
      // 다음 소켓이 만들어질 때까지만 시간을 보낸다. 더 보내면 열리지 않는 연결의 제한 시간까지 함께 지난다.
      while (sockets.all.length === before && failure < MAX_RECONNECT_ATTEMPTS) {
        vi.advanceTimersByTime(TIMING.reconnectBaseMs);
      }
    }
    expect(client.getState().phase).toBe('failed');
    const attempts = sockets.all.length;
    vi.advanceTimersByTime(TIMING.reconnectMaxMs * 10);
    expect(sockets.all).toHaveLength(attempts);

    client.wake();

    expect(sockets.all).toHaveLength(attempts + 1);
    expect(client.getState().phase).toBe('reconnecting');
  });

  it('기기가 인터넷에서 끊겨 있으면 두드리지 않고, 연결이 돌아왔다는 신호에 바로 잇는다', () => {
    const { client, sockets, online } = started();
    online.value = false;

    sockets.latest().drop();
    vi.advanceTimersByTime(TIMING.reconnectMaxMs * 5);
    expect(sockets.all).toHaveLength(1);

    online.value = true;
    client.wake();
    expect(sockets.all).toHaveLength(2);
  });

  it('연결이 열리기도 전에 닫히면 로그인 상태를 확인하게 한다. 이어지는 실패마다 묻지는 않는다', () => {
    const { client, sockets, handshakeFailed } = setup();
    client.start();

    sockets.latest().drop();
    expect(handshakeFailed).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(TIMING.reconnectBaseMs);
    sockets.latest().drop();
    expect(handshakeFailed).toHaveBeenCalledTimes(1);
  });

  it('열렸던 연결이 끊긴 것은 로그인 문제로 보지 않는다', () => {
    const { sockets, handshakeFailed } = started();

    sockets.latest().drop();

    expect(handshakeFailed).not.toHaveBeenCalled();
  });

  it('다른 화면이 대화를 이어받으면 다시 잇지 않는다. 사용자가 고르면 여기서 이어간다', () => {
    const { client, sockets } = started();

    sockets.latest().drop(CLOSE_CODE.takenOver);

    expect(client.getState().phase).toBe('taken_over');
    expect(client.getState().awaitingReply).toBe(false);
    // 여기서 저절로 다시 이으면 두 화면이 서로 대화를 빼앗는다.
    vi.advanceTimersByTime(TIMING.reconnectMaxMs * 5);
    expect(sockets.all).toHaveLength(1);

    client.wake();
    expect(sockets.all).toHaveLength(2);
  });

  it('계정이 사라져 닫힌 연결은 다시 잇지 않고 로그인 상태를 확인하게 한다', () => {
    const { client, sockets, handshakeFailed } = started();

    sockets.latest().drop(CLOSE_CODE.gone);

    expect(handshakeFailed).toHaveBeenCalledTimes(1);
    expect(client.getState().phase).toBe('failed');
    vi.advanceTimersByTime(TIMING.reconnectMaxMs * 5);
    expect(sockets.all).toHaveLength(1);
  });

  it('쉬는 사이에 앞의 대화가 끝나 새 대화로 이어지면 알리고, 고정해 둔 자원은 비운다', () => {
    const { client, sockets } = started();
    sockets.latest().receive({ type: 'resources', items: testResources });

    sockets.latest().drop();
    vi.advanceTimersByTime(TIMING.reconnectBaseMs);
    sockets.latest().open();
    sockets.latest().receive(ready({ conversation_id: '0199a1b2-0000-7000-8000-0000000000ff' }));

    expect(client.getState()).toMatchObject({
      phase: 'ready',
      notice: 'new_conversation',
      resources: null,
      messages: [],
    });
  });

  it('화면을 떠나면 연결을 닫고 다시 잇지 않는다. 돌아오면 이어진다', () => {
    const { client, sockets } = started();

    client.stop();
    expect(sockets.latest().closedByClient).toBe(true);
    vi.advanceTimersByTime(TIMING.reconnectMaxMs * 5);
    expect(sockets.all).toHaveLength(1);

    client.start();
    expect(sockets.all).toHaveLength(2);
  });
});

describe('도움 자원', () => {
  it('resources를 받으면 고정하고, 다시 이어진 뒤에도 서버가 고정했다고 알려주면 그대로 둔다', () => {
    const { client, sockets } = started();

    sockets.latest().receive({ type: 'resources', items: testResources });
    expect(client.getState().resources).toEqual(testResources);

    sockets.latest().drop();
    expect(client.getState().resources).toEqual(testResources);
    vi.advanceTimersByTime(TIMING.reconnectBaseMs);
    sockets.latest().open();
    sockets.latest().receive(ready({ resumed: true, resources_pinned: true }));

    expect(client.getState().resources).toEqual(testResources);
  });

  it('빈 목록이 와도 이미 띄운 번호를 지우지 않는다', () => {
    const { client, sockets } = started();
    sockets.latest().receive({ type: 'resources', items: testResources });

    sockets.latest().receive({ type: 'resources', items: [] });

    expect(client.getState().resources).toEqual(testResources);
  });

  it('서버가 글을 어떻게 판단했는지는 상태 어디에도 없다', () => {
    const { client, sockets, ids } = started();
    client.send('요즘 좀 그래');
    sockets.latest().receive({ ...thinking(ids[0]!, 1), stage: 2, final_stage: 2 });

    expect(JSON.stringify(client.getState())).not.toMatch(/stage/);
  });
});

describe('끝내기', () => {
  it('end를 보내고, ended를 받으면 끝난 상태가 된다. 일기를 기다리는 동안 연결은 열어 둔다', () => {
    const { client, sockets } = started();

    client.end();
    expect(sockets.latest().sentOfType('end')).toEqual([{ type: 'end' }]);
    expect(client.getState().ending).toBe(true);

    sockets.latest().receive({
      type: 'ended',
      reason: 'user',
      record_date: RECORD_DATE,
      diary_expected: true,
    });

    expect(client.getState()).toMatchObject({
      phase: 'ended',
      ending: false,
      ended: { reason: 'user', recordDate: RECORD_DATE, diaryExpected: true },
    });
    expect(sockets.latest().closedByClient).toBe(false);

    sockets.latest().receive({ type: 'diary_ready', record_date: RECORD_DATE });
    expect(client.getState().diaryReady).toBe(true);
    expect(sockets.latest().closedByClient).toBe(true);
  });

  it('끝내기를 두 번 눌러도 end는 한 번만 나간다', () => {
    const { client, sockets } = started();

    client.end();
    client.end();

    expect(sockets.latest().sentOfType('end')).toHaveLength(1);
  });

  it('끝난 뒤에는 다시 연결하지 않고, 글도 보내지 않는다', () => {
    const { client, sockets } = started();
    sockets.latest().receive({
      type: 'ended',
      reason: 'idle',
      record_date: RECORD_DATE,
      diary_expected: false,
    });

    sockets.latest().drop();
    vi.advanceTimersByTime(TIMING.reconnectMaxMs * 5);
    client.wake();

    expect(sockets.all).toHaveLength(1);
    expect(client.send('끝난 뒤의 글')).toBe(false);
    expect(client.getState().ended).toMatchObject({ reason: 'idle', diaryExpected: false });
  });

  it('연결이 끊긴 동안 끝내기를 누르면, 다시 이어지자마자 end를 보낸다', () => {
    const { client, sockets } = started();
    sockets.latest().drop();

    client.end();

    const second = sockets.latest();
    expect(sockets.all).toHaveLength(2);
    second.open();
    second.receive(ready({ resumed: true }));
    expect(second.sentOfType('end')).toHaveLength(1);
  });

  it('end를 보낸 뒤 답을 듣지 못하고 끊기면, 새 대화를 열지 않고 끝난 것으로 본다', () => {
    const { client, sockets } = started();
    client.end();

    sockets.latest().drop();
    vi.advanceTimersByTime(TIMING.reconnectMaxMs * 5);

    expect(sockets.all).toHaveLength(1);
    expect(client.getState()).toMatchObject({
      phase: 'ended',
      ended: { reason: 'user', recordDate: RECORD_DATE, diaryExpected: null },
    });
  });

  it('ended가 제때 오지 않아도 끝난 것으로 본다', () => {
    const { client } = started();
    client.end();

    vi.advanceTimersByTime(TIMING.endTimeoutMs);

    expect(client.getState().phase).toBe('ended');
  });

  it('이미 끝난 대화라는 답을 받으면 끝난 상태가 된다', () => {
    const { client, sockets, ids } = started();
    client.send('늦게 도착한 글');

    sockets.latest().receive({
      type: 'error',
      code: 'conversation_ended',
      client_message_id: ids[0],
    });

    expect(client.getState()).toMatchObject({
      phase: 'ended',
      awaitingReply: false,
      ended: { reason: 'elsewhere', diaryExpected: null },
    });
  });

  it('이 앱이 모르는 까닭으로 끝나도 끝난 상태가 된다', () => {
    const { client, sockets } = started();

    sockets.latest().receive({
      type: 'ended',
      reason: 'maintenance',
      record_date: RECORD_DATE,
      diary_expected: false,
    });

    expect(client.getState().ended?.reason).toBe('other');
  });
});

describe('상태 알리기', () => {
  it('상태가 바뀔 때마다 구독자를 부르고, 구독을 끊으면 더 부르지 않는다', () => {
    const { client, sockets } = setup();
    const listener = vi.fn();
    const unsubscribe = client.subscribe(listener);

    client.start();
    sockets.latest().open();
    sockets.latest().receive(ready());
    const calls = listener.mock.calls.length;
    expect(calls).toBeGreaterThan(0);

    unsubscribe();
    sockets.latest().receive(aiText(0, '안녕하세요'));
    expect(listener).toHaveBeenCalledTimes(calls);
  });
});

describe('음성', () => {
  /** 음성을 쓸 수 있는 서버에 연결해 첫 안부까지 받고, 마이크를 켜 서버가 음성으로 바꿔 준 상태 */
  async function listening(): Promise<Harness> {
    const harness = started(voiceReady());
    await harness.client.startVoice();
    harness.sockets.latest().receive(modeChanged('voice'));
    // 음성이 열리면 첫 한 마디를 저절로 청하고, 서버가 듣고 있다고 답해야 소리가 나간다.
    harness.sockets.latest().receive(serverListening(true));
    return harness;
  }

  it('음성을 켜면 마이크를 열고 음성 방식을 청한다. 서버가 바꿔 준 뒤에야 소리를 올려 보낸다', async () => {
    const { client, sockets, capture } = started(voiceReady());
    const socket = sockets.latest();
    // 소리는 Blob이 아니라 ArrayBuffer로 받는다.
    expect(socket.binaryType).toBe('arraybuffer');

    const starting = client.startVoice();
    expect(client.getState().mic).toBe('starting');
    await starting;

    expect(capture.on).toBe(true);
    expect(client.getState().mic).toBe('on');
    expect(socket.sentOfType('set_mode')).toEqual([{ type: 'set_mode', mode: 'voice' }]);

    // 서버가 아직 글 방식으로 알고 있는 동안의 조각은 보내지 않는다. 보내면 잘못된 메시지로 거절된다.
    capture.frame();
    expect(socket.sentBinary).toHaveLength(0);

    socket.receive(modeChanged('voice'));
    expect(client.getState().mode).toBe('voice');
    // 음성이 열리자마자 첫 한 마디를 청한다. 서버가 듣고 있다고 답하기 전의 조각은 버려질 것이라 보내지 않는다.
    expect(socket.sentOfType('listen')).toEqual([{ type: 'listen', active: true }]);
    capture.frame();
    expect(socket.sentBinary).toHaveLength(0);

    socket.receive(serverListening(true));
    expect(client.getState().listening).toBe(true);
    const frame = capture.frame();
    expect(socket.sentBinary).toEqual([frame]);
    expect(frame.byteLength).toBe(3200);
  });

  it('한 마디가 끝나 서버가 듣기를 멈추면 소리를 보내지 않고, 다시 청해야 보낸다', async () => {
    const { client, sockets, capture } = await listening();
    const socket = sockets.latest();
    const levels: number[] = [];
    client.subscribeLevel((level) => levels.push(level));

    socket.receive(partialTranscript('오늘은 좀'));
    socket.receive(finalTranscript('오늘은 좀 피곤했어', 'u1'));
    socket.receive(serverListening(false));
    expect(client.getState()).toMatchObject({ listening: false, partial: null, mic: 'on' });

    // 멈춘 동안의 혼잣말은 보내지도, 구슬에 보이지도 않는다.
    capture.frame();
    capture.level(0.5);
    expect(socket.sentBinary).toHaveLength(0);
    expect(levels).toEqual([]);

    client.listen();
    expect(socket.sentOfType('listen')).toEqual([
      { type: 'listen', active: true },
      { type: 'listen', active: true },
    ]);
    // 서버가 답하기 전에 또 눌러도 거듭 청하지 않는다... 는 서버가 답한 뒤의 일이다. 답이 오면 소리가 간다.
    socket.receive(serverListening(true));
    const frame = capture.frame();
    expect(socket.sentBinary).toEqual([frame]);
    capture.level(0.5);
    expect(levels).toEqual([0.5]);
  });

  it('내일이 말하는 동안 끊으면 멈추라고 알리고 바로 다음 한 마디를 청한다', async () => {
    const { client, sockets, player } = await listening();
    const socket = sockets.latest();
    socket.receive(finalTranscript('오늘은 좀 피곤했어', 'u1'));
    socket.receive(serverListening(false));
    socket.receive(audioStart(1));
    socket.receiveBinary(100);

    client.interrupt();

    expect(player.clears).toBe(1);
    expect(socket.sentOfType('interrupt')).toEqual([{ type: 'interrupt' }]);
    expect(socket.sentOfType('listen').at(-1)).toEqual({ type: 'listen', active: true });
  });

  it('소리 크기는 상태를 거치지 않고 구독자에게 간다. 내일이 말하는 동안은 재생의 크기만 간다', async () => {
    const { client, sockets, capture, player } = await listening();
    const levels: number[] = [];
    client.subscribeLevel((level) => levels.push(level));
    const before = client.getState();

    capture.level(0.4);
    player.level(0.9);
    expect(levels).toEqual([0.4]);
    expect(client.getState()).toBe(before);

    sockets.latest().receive(audioStart(1));
    capture.level(0.4);
    player.level(0.9);
    expect(levels).toEqual([0.4, 0.9]);
  });

  it('알아들은 말이 자막으로 오고, 끝점이 오면 목록에 올라 답을 기다린다. 소리는 재생기로 가고 다 나가야 끝난다', async () => {
    const { client, sockets, player } = await listening();
    const socket = sockets.latest();

    socket.receive(partialTranscript('오늘은 좀'));
    expect(client.getState().partial).toBe('오늘은 좀');

    socket.receive(finalTranscript('오늘은 좀 피곤했어', 'u1'));
    socket.receive(thinking('u1', 1));
    expect(client.getState()).toMatchObject({ partial: null, awaitingReply: true });
    expect(client.getState().messages.at(-1)).toMatchObject({
      seq: 1,
      text: '오늘은 좀 피곤했어',
      delivery: 'sent',
    });

    socket.receive(aiText(2, '많이 지치셨나 봐요.'));
    socket.receive(audioStart(2, 24_000));
    const first = socket.receiveBinary(4_800);
    const second = socket.receiveBinary(4_800);
    expect(player.started).toEqual([24_000]);
    expect(player.pushed).toEqual([first, second]);
    expect(client.getState()).toMatchObject({ speaking: { seq: 2 }, awaitingReply: false });

    socket.receive(audioEnd(2, 'done'));
    // 서버는 다 보냈지만 받아 둔 소리가 남아 있다. 다 나가야 말하기가 끝난다.
    expect(client.getState().speaking).toEqual({ seq: 2 });
    player.drain();
    expect(client.getState().speaking).toBeNull();
  });

  it('audio_start 없이 온 소리는 버린다', async () => {
    const { sockets, player } = await listening();
    sockets.latest().receiveBinary(100);
    expect(player.pushed).toHaveLength(0);
  });

  it('받아 둔 소리가 너무 오래 나가지 않으면 기다림을 끝낸다', async () => {
    const { client, sockets, player } = await listening();
    const socket = sockets.latest();
    socket.receive(audioStart(2));
    socket.receiveBinary(4_800);
    socket.receive(audioEnd(2, 'done'));
    expect(player.isPlaying()).toBe(true);

    vi.advanceTimersByTime(TIMING.drainTimeoutMs);

    expect(client.getState().speaking).toBeNull();
  });

  it('소리 조각을 하나도 받지 못한 채 끝나면 바로 끝난다', async () => {
    const { client, sockets } = await listening();
    sockets.latest().receive(audioStart(2));
    sockets.latest().receive(audioEnd(2, 'done'));
    expect(client.getState().speaking).toBeNull();
  });

  it('끼어들면 받아 둔 소리를 바로 버리고 서버에 알린다. 늦게 온 조각은 버린다', async () => {
    const { client, sockets, player } = await listening();
    const socket = sockets.latest();
    socket.receive(audioStart(2));
    socket.receiveBinary(4_800);

    client.interrupt();

    expect(player.clears).toBe(1);
    expect(client.getState().speaking).toBeNull();
    expect(socket.sentOfType('interrupt')).toEqual([{ type: 'interrupt' }]);
    socket.receiveBinary(100);
    socket.receive(audioEnd(2, 'interrupted'));
    expect(player.pushed).toHaveLength(1);
  });

  it('서버가 스스로 끊었거나 만들지 못했다고 알리면 받아 둔 소리를 버린다', async () => {
    const { client, sockets, player } = await listening();
    const socket = sockets.latest();
    socket.receive(audioStart(2));
    socket.receiveBinary(4_800);

    socket.receive(audioEnd(2, 'interrupted'));

    expect(player.clears).toBe(1);
    expect(client.getState().speaking).toBeNull();

    socket.receive(audioStart(3));
    socket.receiveBinary(4_800);
    socket.receive(audioEnd(3, 'failed'));
    expect(player.clears).toBe(2);
    expect(client.getState().speaking).toBeNull();
  });

  it('다 말했어요는 음성일 때만 finalize를 보낸다', async () => {
    const { client, sockets } = started(voiceReady());
    client.finalize();
    expect(sockets.latest().sentOfType('finalize')).toHaveLength(0);

    await client.startVoice();
    sockets.latest().receive(modeChanged('voice'));
    client.finalize();

    expect(sockets.latest().sentOfType('finalize')).toEqual([{ type: 'finalize' }]);
  });

  it('음성을 끄면 마이크를 놓고 글 방식을 청한다. 서버의 답은 알림이 아니다', async () => {
    const { client, sockets, capture } = await listening();
    const socket = sockets.latest();

    client.stopVoice();

    expect(capture.on).toBe(false);
    expect(client.getState().mic).toBe('off');
    expect(socket.sentOfType('set_mode').at(-1)).toEqual({ type: 'set_mode', mode: 'chat' });
    expect(socket.sentBinary).toHaveLength(0);

    socket.receive(modeChanged('chat'));
    expect(client.getState()).toMatchObject({ mode: 'chat', notice: null });
  });

  it('음성을 청해 둔 채로 끄면, 서버가 음성으로 바꿔 줘도 바로 글로 되돌린다', async () => {
    const { client, sockets } = started(voiceReady());
    await client.startVoice();
    client.stopVoice();

    sockets.latest().receive(modeChanged('voice'));

    expect(sockets.latest().sentOfType('set_mode')).toEqual([
      { type: 'set_mode', mode: 'voice' },
      { type: 'set_mode', mode: 'chat' },
    ]);
  });

  it('서버가 음성을 쓸 수 없다고 하면 마이크를 놓고 글로 이어간다', async () => {
    const { client, sockets, capture, player } = await listening();
    const socket = sockets.latest();
    socket.receive(audioStart(1));
    socket.receiveBinary(100);

    socket.receive({ type: 'error', code: 'voice_unavailable' });
    socket.receive(modeChanged('chat'));

    expect(capture.on).toBe(false);
    expect(player.clears).toBe(1);
    expect(client.getState()).toMatchObject({
      mode: 'chat',
      mic: 'off',
      speaking: null,
      notice: 'voice_unavailable',
    });
    expect(socket.sentOfType('set_mode')).toHaveLength(1);
    // 글로 이어가는 데 지장이 없다.
    expect(client.send('그럼 글로 할게')).toBe(true);
  });

  it('바라지 않았는데 글로 내려오면 음성이 죽은 것이다. 마이크를 놓고 알린다', async () => {
    const { client, sockets, capture } = await listening();

    sockets.latest().receive(modeChanged('chat'));

    expect(capture.on).toBe(false);
    expect(client.getState()).toMatchObject({
      mode: 'chat',
      mic: 'off',
      notice: 'voice_unavailable',
    });
  });

  it('연결이 끊겼다 이어지면 처음부터 음성으로 열고, 서버가 글로 열어 주면 다시 음성을 청한다', async () => {
    const { client, sockets, capture, player } = await listening();
    sockets.latest().receive(audioStart(1));
    sockets.latest().receiveBinary(100);

    sockets.latest().drop();

    // 소리는 연결과 함께 끊겼다. 마이크는 그대로 켜 둔다.
    expect(player.clears).toBe(1);
    expect(client.getState()).toMatchObject({ phase: 'reconnecting', mic: 'on', speaking: null });
    capture.frame();

    vi.advanceTimersByTime(TIMING.reconnectBaseMs);
    const second = sockets.latest();
    second.open();
    expect(second.sent).toEqual([{ type: 'start', mode: 'voice' }]);

    second.receive(voiceReady({ resumed: true, mode: 'voice' }));
    expect(second.sentOfType('set_mode')).toHaveLength(0);
    // 음성으로 열렸으니 바로 한 마디를 청한다.
    expect(second.sentOfType('listen')).toEqual([{ type: 'listen', active: true }]);
    second.receive(serverListening(true));
    const frame = capture.frame();
    expect(second.sentBinary).toEqual([frame]);

    // 음성을 모르는 옛 서버처럼 글로 열어 주면 다시 청한다.
    second.drop();
    vi.advanceTimersByTime(TIMING.reconnectBaseMs);
    const third = sockets.latest();
    third.open();
    third.receive(voiceReady({ resumed: true, mode: 'chat' }));
    expect(third.sentOfType('set_mode')).toEqual([{ type: 'set_mode', mode: 'voice' }]);
  });

  it('마이크 권한이 거절되면 그렇게 표시하고, 마이크가 없을 때는 다르게 알린다. 다시 켤 수 있다', async () => {
    const { client, sockets, capture } = started(voiceReady());

    capture.failWith = notAllowedError();
    await client.startVoice();
    expect(client.getState()).toMatchObject({ mic: 'denied', notice: 'mic_denied' });
    expect(sockets.latest().sentOfType('set_mode')).toHaveLength(0);

    capture.failWith = notFoundError();
    await client.startVoice();
    expect(client.getState()).toMatchObject({ mic: 'failed', notice: 'mic_missing' });

    capture.failWith = null;
    await client.startVoice();
    expect(client.getState()).toMatchObject({ mic: 'on', notice: null });
  });

  it('마이크가 열렸는데 한동안 소리가 전혀 없으면 알리고, 소리가 들어오면 거둔다', async () => {
    const { client, capture } = await listening();

    // 소리 없는 장치는 정확히 0을 보낸다.
    for (let i = 0; i < 20; i += 1) capture.level(0);
    vi.advanceTimersByTime(TIMING.silentMicMs - 1);
    expect(client.getState().notice).toBeNull();
    vi.advanceTimersByTime(1);
    expect(client.getState().notice).toBe('mic_silent');
    expect(client.getState().mic).toBe('on');

    capture.level(0.2);
    expect(client.getState().notice).toBeNull();

    // 다시 조용해져도 처음부터 다시 잰다.
    capture.level(0);
    vi.advanceTimersByTime(TIMING.silentMicMs - 1);
    expect(client.getState().notice).toBeNull();
  });

  it('내일이 답을 준비하거나 말하는 동안의 조용함은 세지 않는다', async () => {
    const { client, sockets, capture } = await listening();
    const socket = sockets.latest();

    socket.receive(finalTranscript('오늘은 좀 피곤했어', '11111111-1111-4111-8111-111111111111'));
    expect(client.getState().awaitingReply).toBe(true);
    capture.level(0);
    vi.advanceTimersByTime(TIMING.silentMicMs * 2);
    expect(client.getState().notice).toBeNull();

    // 마이크를 끄면 재던 것도 그만둔다.
    client.stopVoice();
    vi.advanceTimersByTime(TIMING.silentMicMs * 2);
    expect(client.getState().notice).toBeNull();
  });

  it('마이크를 켜는 사이에 끄면 켜지자마자 놓는다', async () => {
    const { client, sockets, capture } = started(voiceReady());

    const starting = client.startVoice();
    client.stopVoice();
    await starting;

    expect(capture.on).toBe(false);
    expect(client.getState().mic).toBe('off');
    expect(sockets.latest().sentOfType('set_mode')).toHaveLength(0);
  });

  it('대화가 끝나면 마이크와 재생기를 놓는다', async () => {
    const { client, sockets, capture, player } = await listening();
    sockets.latest().receive(audioStart(1));

    sockets.latest().receive({
      type: 'ended',
      reason: 'user',
      record_date: RECORD_DATE,
      diary_expected: false,
    });

    expect(capture.on).toBe(false);
    expect(player.stops).toBe(1);
    expect(client.getState().mic).toBe('off');
  });

  it('화면을 떠나면 마이크를 놓는다', async () => {
    const { client, capture } = await listening();

    client.stop();

    expect(capture.on).toBe(false);
    expect(client.getState().mic).toBe('off');
  });

  it('마지막으로 고른 방식을 기억한다', async () => {
    const { client } = started(voiceReady());

    await client.startVoice();
    expect(localStorage.getItem('naeil.talk.mode')).toBe('voice');

    client.stopVoice();
    expect(localStorage.getItem('naeil.talk.mode')).toBe('chat');
  });
});
