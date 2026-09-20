import { api, requireBody, send } from '@/api/client';
import { isApiError } from '@/api/errors';
import type { Diary, DiarySummary, RecordDate } from '@/api/types';

/** 한 달("2026-09")의 일기 목록. 날짜순이고, 일기가 없는 날은 목록에 없다. */
export async function fetchDiaryMonth(
  month: string,
  signal?: AbortSignal,
): Promise<DiarySummary[]> {
  const { data, response } = await send(() =>
    api.GET('/api/v1/diaries', { params: { query: { month } }, signal }),
  );
  return requireBody(data, response).items;
}

/** 본인의 모든 일기에서 글자를 찾는다. 최근 날짜부터 온다. */
export async function searchDiaries(q: string, signal?: AbortSignal): Promise<DiarySummary[]> {
  const { data, response } = await send(() =>
    api.GET('/api/v1/diaries', { params: { query: { q } }, signal }),
  );
  return requireBody(data, response).items;
}

/** 그날의 일기. 아직 없으면 null이다. */
export async function fetchDiary(date: RecordDate, signal?: AbortSignal): Promise<Diary | null> {
  try {
    const { data, response } = await send(() =>
      api.GET('/api/v1/diaries/{date}', { params: { path: { date } }, signal }),
    );
    return requireBody(data, response);
  } catch (error) {
    // 404는 실패가 아니라 "그날의 일기가 없다"는 답이다. 대화가 막 끝나 초안을 만드는 중일 때도 이렇게 온다.
    if (isApiError(error) && error.status === 404) return null;
    throw error;
  }
}

/** 글을 그날의 일기로 저장하고 확인한다. 초안을 고치지 않고 그대로 둘 때도 그 글을 그대로 보낸다. */
export async function saveDiary(date: RecordDate, text: string): Promise<Diary> {
  const { data, response } = await send(() =>
    api.PUT('/api/v1/diaries/{date}', { params: { path: { date } }, body: { text } }),
  );
  return requireBody(data, response);
}

/** 하루를 지운다. 그날의 일기, 대화, 대화에서 나온 기록이 함께 지워진다. */
export async function deleteDay(date: RecordDate): Promise<void> {
  await send(() => api.DELETE('/api/v1/days/{date}', { params: { path: { date } } }));
}
