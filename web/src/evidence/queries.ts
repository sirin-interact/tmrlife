import { queryOptions, useMutation, useQueryClient } from '@tanstack/react-query';

import { cancelSignal, fetchDaySignals, uncancelSignal } from '@/api/evidence';
import type { DaySignals, RecordDate } from '@/api/types';
import { withRowCancelled } from '@/evidence/merge';
import { daySignalsKey, invalidateSignalAggregates } from '@/signals/keys';

/** 정리가 끝나기를 기다리는 동안 다시 물어보는 간격 */
export const POLL_INTERVAL_MS = 6_000;

/**
 * 대화가 끝난 뒤 이만큼 지나도 정리가 끝나지 않았으면 기다리기를 그만두고 사람에게 넘긴다.
 * 끝없이 도는 표시를 남겨 두지 않기 위한 값이다. 뒤늦게 끝나는 경우도 있어서 "다시 확인하기"는 늘 열어 둔다.
 */
export const ANALYSIS_GRACE_MS = 3 * 60_000;

export const daySignalsQueryOptions = (date: RecordDate) =>
  queryOptions({
    queryKey: daySignalsKey(date),
    queryFn: ({ signal }) => fetchDaySignals(date, signal),
    // 근거에는 사용자가 한 말이 그대로 담긴다. 화면에서 쓰지 않게 되면 메모리에 남기지 않는다.
    gcTime: 0,
  });

interface SetCancelled {
  signalId: string;
  /** 바꾸려는 값. 참이면 기록에서 빼고, 거짓이면 되돌린다. */
  cancelled: boolean;
}

/**
 * 신호 하나를 기록에서 빼거나 되돌린다.
 *
 * 누른 순간 화면을 먼저 바꾸고(낙관적 반영) 실패하면 되돌린다. 손가락으로 누르는 앱이라
 * 답을 기다리는 동안 아무 일도 일어나지 않으면 사용자는 같은 버튼을 또 누른다.
 * 답이 오면 서버가 다시 합친 하루로 덮는다. 화면의 계산과 서버의 계산이 어긋난 채 남지 않게 한다.
 */
export function useSetSignalCancelled(date: RecordDate) {
  const queryClient = useQueryClient();
  const { queryKey } = daySignalsQueryOptions(date);

  return useMutation({
    mutationFn: ({ signalId, cancelled }: SetCancelled) =>
      cancelled ? cancelSignal(signalId) : uncancelSignal(signalId),
    onMutate: ({ signalId, cancelled }) => {
      const previous = queryClient.getQueryData<DaySignals>(queryKey);
      if (previous !== undefined) {
        queryClient.setQueryData<DaySignals>(
          queryKey,
          withRowCancelled(previous, signalId, cancelled),
        );
      }
      return { previous };
    },
    onError: (_error, _variables, context) => {
      if (context?.previous !== undefined) queryClient.setQueryData(queryKey, context.previous);
    },
    onSuccess: (day) => {
      queryClient.setQueryData(queryKey, day);
      // 빼거나 되돌린 신호는 추세의 일수와 계산 살펴보기의 숫자도 바꾼다.
      // 여기서 낡은 것으로 표시하지 않으면, 곧바로 추세 화면으로 옮겨 간 사람이 아직 옛 점을 본다.
      invalidateSignalAggregates(queryClient);
    },
  });
}
