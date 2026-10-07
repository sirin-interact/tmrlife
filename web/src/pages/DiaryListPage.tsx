import { useQuery } from '@tanstack/react-query';
import { ChevronLeftIcon, ChevronRightIcon, SearchIcon } from 'lucide-react';
import { useId, useState, type FormEvent } from 'react';
import { Link, useLocation, useSearchParams } from 'react-router';

import type { DiarySummary } from '@/api/types';
import { useMe } from '@/auth/session';
import { FieldError } from '@/components/form/FieldError';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { DIARY_TEXT } from '@/content/diaryText';
import { describeError } from '@/content/errorMessages';
import { diaryMonthQueryOptions, diarySearchQueryOptions } from '@/diary/queries';
import { stateValue } from '@/lib/locationState';
import {
  calendarWeeks,
  formatMonth,
  formatRecordDate,
  formatRecordDateShort,
  isMonth,
  monthOf,
  recordDateOf,
  shiftMonth,
} from '@/lib/recordDate';
import { cn } from '@/lib/utils';
import { useNow } from '@/lib/useNow';
import '@/styles/journal.css';

const SEARCH_MAX_LENGTH = 100;

interface LoadFailedProps {
  error: unknown;
  retrying: boolean;
  onRetry: () => void;
}

function LoadFailed({ error, retrying, onRetry }: LoadFailedProps) {
  return (
    <Alert variant="destructive">
      <AlertDescription>
        <p>{describeError(error).message}</p>
        <Button type="button" variant="outline" size="sm" disabled={retrying} onClick={onRetry}>
          {DIARY_TEXT.retry}
        </Button>
      </AlertDescription>
    </Alert>
  );
}

interface EntryListProps {
  items: readonly DiarySummary[];
  /** 검색 결과는 여러 해에 걸칠 수 있어서 해까지 적는다. */
  withYear: boolean;
}

function EntryList({ items, withYear }: EntryListProps) {
  return (
    <ul className="journal-entries">
      {items.map((item) => (
        <li key={item.date}>
          <Link to={`/diary/${item.date}`} className="journal-entry">
            <span className="journal-entry-content">
              <span className="journal-entry-date">
                <time dateTime={item.date}>
                  {withYear ? formatRecordDate(item.date) : formatRecordDateShort(item.date)}
                </time>
                {item.status === 'draft' && (
                  <span className="journal-draft-badge">{DIARY_TEXT.draftBadge}</span>
                )}
              </span>
              <span className="journal-entry-excerpt">
                {item.snippet ??
                  (item.first_line === '' ? DIARY_TEXT.emptyFirstLine : item.first_line)}
              </span>
            </span>
            <ChevronRightIcon className="journal-entry-arrow" aria-hidden="true" />
          </Link>
        </li>
      ))}
    </ul>
  );
}

interface MonthCalendarProps {
  month: string;
  items: readonly DiarySummary[];
}

/** 일기가 있는 날에만 점을 찍은 조용한 달력. 점이 있는 날을 누르면 그날의 일기로 간다. */
function MonthCalendar({ month, items }: MonthCalendarProps) {
  const byDate = new Map(items.map((item) => [item.date, item]));

  return (
    <table aria-label={DIARY_TEXT.calendarLabel} className="journal-month-table">
      <thead>
        <tr>
          {DIARY_TEXT.weekdays.map((weekday) => (
            <th
              key={weekday}
              scope="col"
              className="pb-1 text-xs font-medium text-muted-foreground"
            >
              {weekday}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {calendarWeeks(month).map((week, index) => (
          <tr key={index}>
            {week.map((cell, column) => {
              if (cell === null) return <td key={column} />;
              const entry = byDate.get(cell.date);
              return (
                <td key={column} className="p-0.5">
                  {entry === undefined ? (
                    <span className="flex h-11 items-center justify-center text-sm text-muted-foreground">
                      {cell.day}
                    </span>
                  ) : (
                    <Link
                      to={`/diary/${cell.date}`}
                      aria-label={`${formatRecordDateShort(cell.date)}, ${
                        entry.status === 'draft' ? DIARY_TEXT.hasDraft : DIARY_TEXT.hasDiary
                      }`}
                      className="flex h-11 flex-col items-center justify-center rounded-lg text-sm font-semibold hover:bg-accent"
                    >
                      {cell.day}
                      <span
                        aria-hidden="true"
                        className={cn(
                          'size-1.5 rounded-full',
                          entry.status === 'draft' ? 'border border-primary' : 'bg-primary',
                        )}
                      />
                    </Link>
                  )}
                </td>
              );
            })}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function MonthView({ month, currentMonth }: { month: string; currentMonth: string }) {
  const diaries = useQuery(diaryMonthQueryOptions(month));
  const previous = shiftMonth(month, -1);
  const next = shiftMonth(month, 1);
  const atCurrentMonth = month >= currentMonth;

  return (
    <section aria-labelledby="diary-month-title" className="journal-month-view">
      <div className="journal-calendar-card">
        <div className="journal-month-navigation">
          <Button asChild variant="ghost" size="icon" aria-label={DIARY_TEXT.previousMonth}>
            <Link to={`/diary?month=${previous}`}>
              <ChevronLeftIcon aria-hidden="true" />
            </Link>
          </Button>
          <h2 id="diary-month-title" className="text-lg font-semibold" aria-live="polite">
            {formatMonth(month)}
          </h2>
          {atCurrentMonth ? (
            // 오지 않은 달에는 일기가 없다. 자리는 남겨서 달 이름이 가운데에 머물게 한다.
            <span className="size-12" />
          ) : (
            <Button asChild variant="ghost" size="icon" aria-label={DIARY_TEXT.nextMonth}>
              <Link to={`/diary?month=${next}`}>
                <ChevronRightIcon aria-hidden="true" />
              </Link>
            </Button>
          )}
        </div>

        {diaries.isPending && (
          <p role="status" className="animate-appear-late text-muted-foreground">
            {DIARY_TEXT.loading}
          </p>
        )}
        {diaries.isError && !diaries.data && (
          <LoadFailed
            error={diaries.error}
            retrying={diaries.isFetching}
            onRetry={() => void diaries.refetch()}
          />
        )}
        {diaries.data && <MonthCalendar month={month} items={diaries.data} />}
      </div>
      {diaries.data && (
        <div className="journal-month-entries" key={month}>
          <div className="journal-section-heading">
            <h3>{DIARY_TEXT.entriesTitle}</h3>
            <span>{DIARY_TEXT.monthCount(diaries.data.length)}</span>
          </div>
          {diaries.data.length === 0 ? (
            <div className="journal-empty">
              <p>{DIARY_TEXT.emptyMonth}</p>
              <Button asChild variant="outline">
                <Link to="/talk">{DIARY_TEXT.startTalking}</Link>
              </Button>
            </div>
          ) : (
            // 달력은 날짜순이지만 목록은 최근 날이 위에 오는 편이 찾기 쉽다.
            <EntryList items={[...diaries.data].reverse()} withYear={false} />
          )}
        </div>
      )}
    </section>
  );
}

function SearchResults({ q }: { q: string }) {
  const results = useQuery(diarySearchQueryOptions(q));

  if (results.isPending) {
    return (
      <p role="status" className="animate-appear-late text-muted-foreground">
        {DIARY_TEXT.loading}
      </p>
    );
  }
  if (results.isError && !results.data) {
    return (
      <LoadFailed
        error={results.error}
        retrying={results.isFetching}
        onRetry={() => void results.refetch()}
      />
    );
  }

  const items = results.data ?? [];
  return (
    <section className="flex flex-col gap-4">
      <p role="status" className="text-muted-foreground">
        {items.length === 0 ? DIARY_TEXT.searchEmpty : DIARY_TEXT.searchResults(items.length)}
      </p>
      <EntryList items={items} withYear />
    </section>
  );
}

/** 일기장. 달마다 일기를 보고, 찾고 싶은 말로 모든 일기에서 찾는다. */
export function DiaryListPage() {
  const me = useMe();
  const now = useNow();
  const location = useLocation();
  const [searchParams] = useSearchParams();
  const searchInputId = useId();
  // 검색어는 주소에 두지 않는다. 일기에서 찾는 말은 그 자체로 사적인 글이고, 주소는 브라우저 기록에 남는다.
  const [draftQuery, setDraftQuery] = useState('');
  const [query, setQuery] = useState('');
  const [searchError, setSearchError] = useState<string | undefined>(undefined);

  const currentMonth = monthOf(recordDateOf(now, me.data?.user.timezone));
  const requested = searchParams.get('month');
  // 주소의 값은 꼴을 믿지 않는다. 틀렸거나 아직 오지 않은 달이면 이번 달을 보여 준다.
  const month =
    requested !== null && isMonth(requested) && requested <= currentMonth
      ? requested
      : currentMonth;

  const notice = stateValue(location.state, 'notice');

  function handleSearch(event: FormEvent) {
    event.preventDefault();
    const trimmed = draftQuery.trim();
    if (trimmed === '') {
      setSearchError(DIARY_TEXT.searchBlank);
      return;
    }
    if (Array.from(trimmed).length > SEARCH_MAX_LENGTH) {
      setSearchError(DIARY_TEXT.searchTooLong);
      return;
    }
    setSearchError(undefined);
    setQuery(trimmed);
  }

  function clearSearch() {
    setDraftQuery('');
    setQuery('');
    setSearchError(undefined);
  }

  return (
    <div className="journal-page">
      <title>일기장 · 내일</title>
      <header className="journal-page-heading">
        <h1>{DIARY_TEXT.listTitle}</h1>
      </header>

      {typeof notice === 'string' && (
        <p role="status" className="rounded-xl border bg-card px-5 py-4">
          {notice}
        </p>
      )}

      <form role="search" onSubmit={handleSearch} noValidate className="journal-search-form">
        <label htmlFor={searchInputId} className="sr-only">
          {DIARY_TEXT.searchLabel}
        </label>
        <div className="journal-search-field">
          <Input
            id={searchInputId}
            type="search"
            value={draftQuery}
            onChange={(event) => setDraftQuery(event.target.value)}
            placeholder={DIARY_TEXT.searchPlaceholder}
            autoComplete="off"
            enterKeyHint="search"
            aria-invalid={searchError !== undefined}
            aria-describedby={searchError === undefined ? undefined : 'diary-search-error'}
          />
          <Button type="submit" variant="outline" size="icon" aria-label={DIARY_TEXT.searchSubmit}>
            <SearchIcon aria-hidden="true" />
          </Button>
        </div>
        <FieldError id="diary-search-error" message={searchError} />
        {query !== '' && (
          <Button type="button" variant="link" onClick={clearSearch} className="self-start px-0">
            {DIARY_TEXT.searchClear}
          </Button>
        )}
      </form>

      {query === '' ? (
        <MonthView month={month} currentMonth={currentMonth} />
      ) : (
        <SearchResults q={query} />
      )}
    </div>
  );
}
