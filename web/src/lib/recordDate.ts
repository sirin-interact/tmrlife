/**
 * 기록 날짜("2026-09-20")와 달("2026-09")을 다룬다.
 *
 * 기록 날짜는 달력 날짜가 아니다. 하루는 사용자의 시간대로 새벽 4시에 바뀐다. 새벽 1시의 대화는 전날의 기록이다.
 * 어느 기록이 어느 날에 속하는지는 서버가 정한다. 일기를 읽고 쓰고 지울 때는 언제나 서버가 알려준 날짜를 쓴다.
 * 이 파일의 recordDateOf는 서버에 묻기 전에 화면이 "오늘"을 말해야 하는 자리(처음 화면의 날짜, 일기장이 처음 여는 달)에만 쓴다.
 */

/** 하루가 바뀌는 현지 시각(시). 서버의 규칙과 같은 값이어야 한다. */
export const DAY_START_HOUR = 4;

const RECORD_DATE_SHAPE = /^(\d{4})-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$/;
const MONTH_SHAPE = /^(\d{4})-(0[1-9]|1[0-2])$/;

interface WallClock {
  year: number;
  month: number;
  day: number;
  hour: number;
}

function wallClockParts(now: Date, timeZone: string | undefined): Intl.DateTimeFormatPart[] {
  const options: Intl.DateTimeFormatOptions = {
    year: 'numeric',
    month: 'numeric',
    day: 'numeric',
    hour: 'numeric',
    hourCycle: 'h23',
  };
  try {
    return new Intl.DateTimeFormat('en-US', { ...options, timeZone }).formatToParts(now);
  } catch {
    // 서버가 아는 시간대 이름을 이 브라우저가 모를 수 있다. 그때는 기기의 시간대로 읽는다.
    return new Intl.DateTimeFormat('en-US', options).formatToParts(now);
  }
}

function wallClock(now: Date, timeZone: string | undefined): WallClock {
  const values: Partial<Record<Intl.DateTimeFormatPartTypes, number>> = {};
  for (const part of wallClockParts(now, timeZone)) {
    values[part.type] = Number.parseInt(part.value, 10);
  }
  return {
    year: values.year ?? now.getFullYear(),
    month: values.month ?? now.getMonth() + 1,
    day: values.day ?? now.getDate(),
    // 자정을 24시로 적는 옛 브라우저가 있다.
    hour: (values.hour ?? now.getHours()) % 24,
  };
}

function isoDate(year: number, monthIndex: number, day: number): string {
  // 달과 날이 범위를 넘으면(0일, 13월) Date가 앞뒤 달로 넘겨서 계산해 준다.
  return new Date(Date.UTC(year, monthIndex, day)).toISOString().slice(0, 10);
}

/**
 * 지금이 그 시간대에서 어느 기록 날짜인지. 벽시계가 04:00 전이면 전날이다.
 * 일광 절약 시간으로 시계가 04:00 앞뒤에서 바뀌는 드문 시간대에서는 서버의 정의와 한 시간쯤 어긋날 수 있다.
 * 그래서 이 값을 서버로 보내는 날짜로 쓰지 않는다.
 */
export function recordDateOf(now: Date, timeZone?: string): string {
  const { year, month, day, hour } = wallClock(now, timeZone);
  return isoDate(year, month - 1, hour < DAY_START_HOUR ? day - 1 : day);
}

/** 주소에 담겨 온 값이 기록 날짜 꼴이고 달력에 있는 날인지 */
export function isRecordDate(value: string): boolean {
  const match = RECORD_DATE_SHAPE.exec(value);
  if (!match) return false;
  // 2월 30일 같은 값은 Date가 다른 날로 넘겨 버린다. 되돌려 적었을 때 같은 글자인지로 가린다.
  return isoDate(Number(match[1]), Number(match[2]) - 1, Number(match[3])) === value;
}

export function isMonth(value: string): boolean {
  return MONTH_SHAPE.test(value);
}

/** 기록 날짜가 속한 달("2026-09") */
export function monthOf(recordDate: string): string {
  return recordDate.slice(0, 7);
}

/** 달을 앞뒤로 옮긴다. shiftMonth("2026-01", -1)은 "2025-12"다. */
export function shiftMonth(month: string, by: number): string {
  const [year, monthNumber] = month.split('-').map(Number) as [number, number];
  return isoDate(year, monthNumber - 1 + by, 1).slice(0, 7);
}

function utcDate(recordDate: string): Date {
  return new Date(`${recordDate}T00:00:00.000Z`);
}

function formatUtc(options: Intl.DateTimeFormatOptions, date: Date): string {
  // 기록 날짜에는 시각도 시간대도 없다. UTC 자정으로 놓고 UTC로 적어야 기기의 시간대 때문에 하루가 밀리지 않는다.
  return new Intl.DateTimeFormat('ko-KR', { ...options, timeZone: 'UTC' }).format(date);
}

/** "2026년 9월 20일 일요일" */
export function formatRecordDate(recordDate: string): string {
  return formatUtc({ dateStyle: 'full' }, utcDate(recordDate));
}

/** "9월 20일 일요일". 해가 문맥으로 분명한 자리에 쓴다. */
export function formatRecordDateShort(recordDate: string): string {
  return formatUtc({ month: 'long', day: 'numeric', weekday: 'long' }, utcDate(recordDate));
}

/** "2026년 9월" */
export function formatMonth(month: string): string {
  return formatUtc({ year: 'numeric', month: 'long' }, utcDate(`${month}-01`));
}

export interface CalendarDay {
  /** 기록 날짜 */
  date: string;
  /** 그 달의 며칠인지 */
  day: number;
}

/**
 * 한 달을 일요일부터 시작하는 주 단위로 나눈다. 그 달에 속하지 않는 칸은 null이다.
 */
export function calendarWeeks(month: string): Array<Array<CalendarDay | null>> {
  const [year, monthNumber] = month.split('-').map(Number) as [number, number];
  const leading = new Date(Date.UTC(year, monthNumber - 1, 1)).getUTCDay();
  const daysInMonth = new Date(Date.UTC(year, monthNumber, 0)).getUTCDate();

  const cells: Array<CalendarDay | null> = Array.from({ length: leading }, () => null);
  for (let day = 1; day <= daysInMonth; day += 1) {
    cells.push({ date: isoDate(year, monthNumber - 1, day), day });
  }
  while (cells.length % 7 !== 0) cells.push(null);

  const weeks: Array<Array<CalendarDay | null>> = [];
  for (let index = 0; index < cells.length; index += 7) {
    weeks.push(cells.slice(index, index + 7));
  }
  return weeks;
}
