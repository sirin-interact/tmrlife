import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useState, useSyncExternalStore } from 'react';

import type { ConversationMode } from '@/api/types';
import { meQueryKey } from '@/auth/queryKeys';
import { ConversationClient } from '@/talk/conversationClient';
import type { ConversationState } from '@/talk/conversationState';
import { conversationUrl } from '@/talk/messages';
import { readPreferredMode } from '@/talk/modePreference';

export interface Conversation {
  state: ConversationState;
  /** 화면을 열 때 사용자가 마지막으로 골랐던 방식. 음성이면 구슬을 눌러 시작하게 권한다. */
  preferredMode: ConversationMode;
  send: (text: string) => boolean;
  retry: (clientMessageId: string) => void;
  end: () => void;
  reconnect: () => void;
  dismissNotice: () => void;
  /** 사용자 동작 처리기 안에서 바로 불러야 한다. 그래야 iOS가 오디오를 열어 준다. */
  startVoice: () => void;
  stopVoice: () => void;
  /** 내일의 말을 끊는다. */
  interrupt: () => void;
  /** 구슬을 눌러 다음 한 마디를 듣게 한다 */
  listen: () => void;
  /** "다 말했어요" */
  finalize: () => void;
  /** 소리 크기(0~1). 초당 수십 번 오므로 상태가 아니라 CSS 변수로 바로 넣는다. */
  subscribeLevel: (listener: (level: number) => void) => () => void;
}

/**
 * 대화 채널을 화면에 잇는다. 화면이 보이는 동안 연결을 지키고, 화면을 떠나면 닫는다.
 * 닫아도 대화는 서버에 한동안 열려 있다. 돌아오면 지난 발화와 함께 이어진다.
 */
export function useConversation(): Conversation {
  const queryClient = useQueryClient();
  const [client] = useState(
    () =>
      new ConversationClient({
        url: conversationUrl(window.location),
        // 브라우저는 거절된 연결의 까닭을 알려주지 않는다. 세션이 끝난 것이라면 로그인 상태를 다시 확인하는 것으로 드러나고,
        // 경로 보호가 로그인 화면으로 보낸다. 로그인하면 이 화면으로 돌아온다.
        onHandshakeFailed: () => void queryClient.invalidateQueries({ queryKey: meQueryKey }),
      }),
  );
  const [preferredMode] = useState(readPreferredMode);
  const state = useSyncExternalStore(client.subscribe, client.getState);

  useEffect(() => {
    // 한 박자 늦게 연결한다. 개발 모드는 화면을 올렸다가 곧바로 내리고 다시 올리는데,
    // 그때마다 연결을 열었다 닫으면 서버에 쓸모없는 연결이 하나씩 생긴다.
    const timer = window.setTimeout(() => client.start(), 0);

    // 폰은 앱이 뒤로 가 있는 동안 연결을 끊는다. 돌아오는 순간과 인터넷이 돌아오는 순간에 기다리지 않고 바로 잇는다.
    const wake = () => client.wake();
    const wakeWhenVisible = () => {
      if (document.visibilityState === 'visible') client.wake();
    };
    window.addEventListener('online', wake);
    document.addEventListener('visibilitychange', wakeWhenVisible);

    return () => {
      window.clearTimeout(timer);
      window.removeEventListener('online', wake);
      document.removeEventListener('visibilitychange', wakeWhenVisible);
      client.stop();
    };
  }, [client]);

  return {
    state,
    preferredMode,
    send: (text) => client.send(text),
    retry: (clientMessageId) => client.retry(clientMessageId),
    end: () => client.end(),
    reconnect: () => client.wake(),
    dismissNotice: () => client.dismissNotice(),
    startVoice: () => void client.startVoice(),
    stopVoice: () => client.stopVoice(),
    interrupt: () => client.interrupt(),
    listen: () => client.listen(),
    finalize: () => client.finalize(),
    subscribeLevel: client.subscribeLevel,
  };
}
