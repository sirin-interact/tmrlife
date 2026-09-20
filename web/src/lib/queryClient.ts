import { MutationCache, QueryCache, QueryClient } from '@tanstack/react-query';

import { isApiError } from '@/api/errors';
import { meQueryKey } from '@/auth/queryKeys';

// 4xx는 다시 보내도 같은 답이 온다. 연결 실패와 서버 쪽 문제만 한 번 더 해 본다.
function shouldRetry(failureCount: number, error: unknown): boolean {
  if (isApiError(error) && error.status >= 400 && error.status < 500) return false;
  return failureCount < 1;
}

export function createQueryClient(): QueryClient {
  // 쓰던 도중에 세션이 끝나면 어느 요청이든 401 unauthenticated로 돌아온다.
  // 그때 로그인 상태를 비워 두면 경로 보호가 로그인 화면으로 보내고, 로그인 뒤에는 보던 화면으로 돌아온다.
  function signOutWhenSessionEnded(error: unknown): void {
    if (isApiError(error) && error.code === 'unauthenticated') {
      client.setQueryData(meQueryKey, null);
    }
  }

  const client = new QueryClient({
    queryCache: new QueryCache({ onError: signOutWhenSessionEnded }),
    mutationCache: new MutationCache({ onError: signOutWhenSessionEnded }),
    defaultOptions: {
      queries: {
        // 기본값('online')은 브라우저가 연결이 끊겼다고 알려 온 동안 요청을 보내지 않고 붙잡아 둔다.
        // 그러면 화면은 실패 안내 없이 "기다리는 중"에 머문다. 이 앱에는 연결이 돌아오면 이어서 보낼 대기열이 없다.
        // 언제나 바로 보내고, 실패하면 바로 알린다.
        networkMode: 'always',
        staleTime: 30_000,
        // 응답에는 개인 기록이 담긴다. 화면에서 쓰지 않는 데이터를 메모리에 오래 붙잡아 두지 않는다.
        // 같은 이유로 조회 결과를 localStorage 같은 저장소에 보관하는 기능도 붙이지 않는다.
        gcTime: 5 * 60_000,
        retry: shouldRetry,
      },
      mutations: {
        // 붙잡아 둔 가입 요청이 한참 뒤에 연결이 돌아오는 순간 저절로 나가는 일도 함께 막는다.
        networkMode: 'always',
        retry: false,
      },
    },
  });

  return client;
}
