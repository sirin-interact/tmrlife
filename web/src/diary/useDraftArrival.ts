import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useState } from 'react';

import type { RecordDate } from '@/api/types';
import { diaryQueryOptions } from '@/diary/queries';

export const DRAFT_POLL_INTERVAL_MS = 3_000;
/** 이만큼 확인해도 없으면 오래 걸린다고 알린다(약 1분). */
export const DRAFT_POLLS_BEFORE_SLOW = 20;
/** 이만큼 확인해도 없으면 그만 확인한다(약 3분). */
export const DRAFT_POLLS_MAX = 60;

export interface DraftArrival {
  arrived: boolean;
  slow: boolean;
}

/**
 * 대화가 끝난 뒤 그날의 일기 초안이 생겼는지 확인한다.
 * 보통은 대화 채널이 먼저 알려준다. 그 연결이 먼저 끊기면 소식이 오지 않으므로 일기를 직접 읽어 본다.
 *
 * 그날 이미 일기가 있었을 수 있다(하루에 두 번째 대화). 그래서 "있다"가 아니라 "처음 읽었을 때와 달라졌다"를 본다.
 */
export function useDraftArrival(recordDate: RecordDate | null, enabled: boolean): DraftArrival {
  const queryClient = useQueryClient();
  const [arrival, setArrival] = useState<DraftArrival>({ arrived: false, slow: false });

  useEffect(() => {
    if (!enabled || recordDate === null) return;

    const date = recordDate;
    let cancelled = false;
    let timer: number | undefined;
    let polls = 0;
    // undefined는 아직 한 번도 읽지 못했다는 뜻이다. null은 읽었는데 일기가 없었다는 뜻이다.
    let baseline: string | null | undefined;

    async function poll() {
      polls += 1;
      try {
        const diary = await queryClient.fetchQuery({
          ...diaryQueryOptions(date),
          staleTime: 0,
          retry: false,
        });
        if (cancelled) return;
        const seen = diary?.updated_at ?? null;
        if (baseline === undefined) baseline = seen;
        else if (seen !== baseline) {
          setArrival({ arrived: true, slow: false });
          return;
        }
      } catch {
        // 확인에 실패해도 할 일이 없다. 다음 차례에 다시 읽는다. 세션이 끝난 경우는 조회 계층이 로그인 화면으로 보낸다.
        if (cancelled) return;
      }

      if (polls >= DRAFT_POLLS_MAX) return;
      if (polls >= DRAFT_POLLS_BEFORE_SLOW) setArrival({ arrived: false, slow: true });
      timer = window.setTimeout(() => void poll(), DRAFT_POLL_INTERVAL_MS);
    }

    void poll();
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [enabled, queryClient, recordDate]);

  return arrival;
}
