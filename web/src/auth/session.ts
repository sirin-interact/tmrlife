import {
  queryOptions,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from '@tanstack/react-query';
import { useNavigate } from 'react-router';

import { fetchAuthRequirements, fetchMe, login, logout, signup } from '@/api/auth';
import { ApiError } from '@/api/errors';
import type { LoginRequest, Me, SignupRequest } from '@/api/types';
import { authRequirementsQueryKey, meQueryKey } from '@/auth/queryKeys';

// 값이 null이면 "로그인하지 않았다"는 확정된 답이다. 아직 모르는 상태(undefined)와 구분한다.
// 앱으로 돌아올 때마다 다시 확인하므로(창에 초점이 올 때의 기본 동작) 다른 탭에서 로그아웃한 것도 곧 반영된다.
export const meQueryOptions = queryOptions({
  queryKey: meQueryKey,
  queryFn: ({ signal }) => fetchMe(signal),
});

export const authRequirementsQueryOptions = queryOptions({
  queryKey: authRequirementsQueryKey,
  queryFn: ({ signal }) => fetchAuthRequirements(signal),
  // 가입 규칙은 자주 바뀌지 않는다. 가입 화면에 머무는 동안 창을 오갈 때마다 다시 받지 않는다.
  staleTime: 5 * 60_000,
});

export function useMe() {
  return useQuery(meQueryOptions);
}

export function useAuthRequirements() {
  return useQuery(authRequirementsQueryOptions);
}

/**
 * 로그인이나 가입이 성공한 직후에 세션이 실제로 이어지는지 확인하고 로그인 상태를 채운다.
 * 로그인 응답에는 설정이 없어서 어차피 한 번은 받아 와야 하고, 브라우저가 쿠키를 막고 있으면
 * 여기서 드러난다. 확인하지 않으면 "로그인 성공" 뒤에 곧바로 로그인 화면으로 튕기는 이유를 사용자가 알 수 없다.
 */
async function confirmSession(queryClient: QueryClient): Promise<Me> {
  // 로그인 전에 출발한 조회가 늦게 도착해서 "로그인 안 됨"으로 덮어쓰지 않게 먼저 멈춘다.
  await queryClient.cancelQueries({ queryKey: meQueryKey });
  const me = await queryClient.fetchQuery({ ...meQueryOptions, staleTime: 0 });
  if (me === null) throw new ApiError({ code: 'session_not_kept', status: 0 });
  return me;
}

export function useLogin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: LoginRequest) => {
      await login(body);
      return confirmSession(queryClient);
    },
  });
}

export function useSignup() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: SignupRequest) => {
      await signup(body);
      return confirmSession(queryClient);
    },
  });
}

/**
 * 로그아웃한다. 성공하면 로그인 상태를 비우고 로그인 화면으로 옮긴다.
 *
 * 화면을 옮기는 일까지 여기서 하는 까닭: 세션을 비우면 경로 보호가 곧바로 로그인 화면으로 보내면서
 * "보던 주소"를 함께 넘긴다. 세션이 도중에 끝난 사람에게는 돌아갈 자리라서 필요한 값이지만,
 * 스스로 로그아웃한 사람에게는 필요 없고, 같은 기기를 다음 사람이 이어서 쓰면 앞사람이 보던 화면으로 데려간다.
 * 그래서 비우는 일과 옮기는 일을 한 박자에 한다. 둘을 나누면 그 사이에 화면이 한 번 그려져
 * 경로 보호가 먼저 움직이고, 어느 쪽이 마지막에 남는지가 그때그때 달라진다.
 */
export function useLogout() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  return useMutation({
    mutationFn: logout,
    onSuccess: () => {
      void navigate('/login', { replace: true, state: null });
      // 로그아웃 전에 출발한 조회가 늦게 도착해서 로그인 상태를 되살리지 않게 취소를 먼저 건다.
      // 기다리지는 않는다. 기다리면 아래 두 줄이 위 이동과 다른 박자가 된다.
      void queryClient.cancelQueries({ queryKey: meQueryKey });
      queryClient.setQueryData(meQueryKey, null);
      // 남은 조회 결과에는 방금 로그아웃한 사람의 기록이 들어 있다. 같은 기기를 다른 사람이 이어서 쓸 수 있다.
      queryClient.removeQueries({ predicate: (query) => query.queryKey[0] !== meQueryKey[0] });
    },
  });
}
