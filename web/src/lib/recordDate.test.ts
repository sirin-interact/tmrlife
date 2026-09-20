import { describe, expect, it } from 'vitest';

import {
  calendarWeeks,
  formatMonth,
  formatRecordDate,
  formatRecordDateShort,
  isMonth,
  isRecordDate,
  monthOf,
  recordDateOf,
  shiftMonth,
} from '@/lib/recordDate';

describe('지금이 어느 기록 날짜인지', () => {
  it.each([
    // 서울은 UTC보다 아홉 시간 빠르다.
    ['저녁 8시는 그날이다', '2026-09-20T11:00:00.000Z', 'Asia/Seoul', '2026-09-20'],
    [
      '자정을 넘긴 새벽 1시 30분은 아직 전날이다',
      '2026-09-20T16:30:00.000Z',
      'Asia/Seoul',
      '2026-09-20',
    ],
    ['새벽 3시 59분 59초까지 전날이다', '2026-09-20T18:59:59.999Z', 'Asia/Seoul', '2026-09-20'],
    ['새벽 4시 정각부터 새 날이다', '2026-09-20T19:00:00.000Z', 'Asia/Seoul', '2026-09-21'],
    [
      '달의 첫날 새벽은 지난달 마지막 날이다',
      '2026-09-30T17:00:00.000Z',
      'Asia/Seoul',
      '2026-09-30',
    ],
    [
      '새해 첫날 새벽은 지난해 마지막 날이다',
      '2026-12-31T16:00:00.000Z',
      'Asia/Seoul',
      '2026-12-31',
    ],
    [
      '3월 1일 새벽은 2월의 마지막 날이다(윤년이 아닌 해)',
      '2027-02-28T17:00:00.000Z',
      'Asia/Seoul',
      '2027-02-28',
    ],
    [
      '같은 순간도 시간대가 다르면 날짜가 다르다',
      '2026-09-20T19:00:00.000Z',
      'America/New_York',
      '2026-09-20',
    ],
    ['뉴욕의 새벽 2시는 전날이다', '2026-09-20T06:00:00.000Z', 'America/New_York', '2026-09-19'],
  ])('%s', (_name, instant, timeZone, expected) => {
    expect(recordDateOf(new Date(instant), timeZone)).toBe(expected);
  });

  it('브라우저가 모르는 시간대면 던지지 않고 기기의 시간대로 읽는다', () => {
    expect(recordDateOf(new Date('2026-09-20T11:00:00.000Z'), 'Mars/Olympus_Mons')).toMatch(
      /^2026-09-(19|20|21)$/,
    );
  });
});

describe('기록 날짜와 달의 꼴', () => {
  it.each(['2026-09-20', '2028-02-29', '2026-12-31'])('%s은 기록 날짜다', (value) => {
    expect(isRecordDate(value)).toBe(true);
  });

  it.each(['2026-9-20', '2026-02-30', '2027-02-29', '2026-13-01', '2026-09-20T00:00', '', 'today'])(
    '"%s"은 기록 날짜가 아니다',
    (value) => {
      expect(isRecordDate(value)).toBe(false);
    },
  );

  it('달은 YYYY-MM 꼴만 받는다', () => {
    expect(isMonth('2026-09')).toBe(true);
    expect(isMonth('2026-13')).toBe(false);
    expect(isMonth('2026-9')).toBe(false);
    expect(isMonth('2026-09-20')).toBe(false);
  });

  it('기록 날짜에서 달을 꺼내고, 달을 앞뒤로 옮긴다', () => {
    expect(monthOf('2026-09-20')).toBe('2026-09');
    expect(shiftMonth('2026-09', 1)).toBe('2026-10');
    expect(shiftMonth('2026-12', 1)).toBe('2027-01');
    expect(shiftMonth('2026-01', -1)).toBe('2025-12');
  });
});

describe('기록 날짜를 한국어로 적기', () => {
  it('기기의 시간대와 상관없이 그 날짜 그대로 적는다', () => {
    expect(formatRecordDate('2026-09-20')).toBe('2026년 9월 20일 일요일');
    expect(formatRecordDateShort('2026-09-21')).toBe('9월 21일 월요일');
    expect(formatMonth('2026-09')).toBe('2026년 9월');
  });
});

describe('달력', () => {
  it('일요일부터 시작하는 주로 나누고, 그 달이 아닌 칸은 비운다', () => {
    // 2026년 9월 1일은 화요일, 30일은 수요일이다.
    const weeks = calendarWeeks('2026-09');

    expect(weeks).toHaveLength(5);
    expect(weeks.every((week) => week.length === 7)).toBe(true);
    expect(weeks[0]).toEqual([
      null,
      null,
      { date: '2026-09-01', day: 1 },
      { date: '2026-09-02', day: 2 },
      { date: '2026-09-03', day: 3 },
      { date: '2026-09-04', day: 4 },
      { date: '2026-09-05', day: 5 },
    ]);
    expect(weeks[4]?.slice(3)).toEqual([{ date: '2026-09-30', day: 30 }, null, null, null]);
  });

  it('윤년의 2월은 29일까지다', () => {
    const days = calendarWeeks('2028-02')
      .flat()
      .filter((cell) => cell !== null);

    expect(days).toHaveLength(29);
    expect(days.at(-1)).toEqual({ date: '2028-02-29', day: 29 });
  });
});
