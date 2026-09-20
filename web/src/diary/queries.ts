import { queryOptions, useMutation, useQueryClient } from '@tanstack/react-query';

import { deleteDay, fetchDiary, fetchDiaryMonth, saveDiary, searchDiaries } from '@/api/diaries';
import type { RecordDate } from '@/api/types';

// 일기에 관한 조회는 모두 'diaries'로 시작한다. 일기가 바뀌면 이 앞머리로 한꺼번에 낡은 것으로 표시한다.
const DIARIES = 'diaries';

export const diaryQueryOptions = (date: RecordDate) =>
  queryOptions({
    queryKey: [DIARIES, 'day', date] as const,
    queryFn: ({ signal }) => fetchDiary(date, signal),
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
    },
  });
}
