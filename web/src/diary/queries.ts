import { queryOptions, useMutation, useQueryClient } from '@tanstack/react-query';

import { deleteDay, fetchDiary, fetchDiaryMonth, saveDiary, searchDiaries } from '@/api/diaries';
import type { RecordDate } from '@/api/types';
import { invalidateSignalViews } from '@/signals/keys';

// 일기에 관한 조회는 모두 'diaries'로 시작한다. 일기가 바뀌면 이 앞머리로 한꺼번에 낡은 것으로 표시한다.
const DIARIES = 'diaries';

export const diaryQueryOptions = (date: RecordDate) =>
  queryOptions({
    queryKey: [DIARIES, 'day', date] as const,
    queryFn: ({ signal }) => fetchDiary(date, signal),
  });

/**
 * 대화를 마친 뒤 그날의 초안이 도착했는지 살피는 조회. 읽는 것은 그날의 일기와 같지만 키를 따로 둔다.
 *
 * 같은 키를 쓰면 "아직 일기가 없다"는 답이 그날의 일기 조회에 남는다. 조회 결과는 잠시 그대로 쓰이므로,
 * 곧이어 초안이 도착해 일기 화면으로 옮겨 가도 그 화면은 남아 있는 답을 보고 "이 날의 일기가 아직 없어요"를 보여 준다.
 */
export const diaryArrivalQueryOptions = (date: RecordDate) =>
  queryOptions({
    queryKey: [DIARIES, 'arrival', date] as const,
    queryFn: ({ signal }) => fetchDiary(date, signal),
    // 살피는 동안에만 쓰는 값이다. 일기 글이 메모리에 남지 않게 바로 버린다.
    gcTime: 0,
  });

export const diaryMonthQueryOptions = (month: string) =>
  queryOptions({
    queryKey: [DIARIES, 'month', month] as const,
    queryFn: ({ signal }) => fetchDiaryMonth(month, signal),
  });

export const diarySearchQueryOptions = (q: string) =>
  queryOptions({
    queryKey: [DIARIES, 'search', q] as const,
    queryFn: ({ signal }) => searchDiaries(q, signal),
    // 검색어와 결과에는 일기의 글이 담긴다. 화면에서 쓰지 않게 되면 바로 버린다.
    gcTime: 0,
  });

export function useSaveDiary(date: RecordDate) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (text: string) => saveDiary(date, text),
    onSuccess: (diary) => {
      queryClient.setQueryData(diaryQueryOptions(date).queryKey, diary);
      // 목록의 첫 줄과 상태, 검색 결과가 함께 바뀐다.
      void queryClient.invalidateQueries({ queryKey: [DIARIES, 'month'] });
      void queryClient.invalidateQueries({ queryKey: [DIARIES, 'search'] });
    },
  });
}

export function useDeleteDay(date: RecordDate) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => deleteDay(date),
    onSuccess: () => {
      // 지운 날의 글을 메모리에 남겨 두지 않는다.
      queryClient.removeQueries({ queryKey: diaryQueryOptions(date).queryKey });
      void queryClient.invalidateQueries({ queryKey: [DIARIES] });
      // 그날의 대화와 신호도 함께 지워졌다. 추세와 근거 화면이 없는 기록을 계속 보여 주지 않게 한다.
      invalidateSignalViews(queryClient);
    },
  });
}
