import { useQuery } from '@tanstack/react-query';
import { ArrowLeftIcon, CheckIcon, PenLineIcon } from 'lucide-react';
import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { Link, useLocation, useNavigate, useParams } from 'react-router';

import { isApiError } from '@/api/errors';
import type { Diary, RecordDate } from '@/api/types';
import { ConfirmPanel } from '@/components/ConfirmPanel';
import { FieldError } from '@/components/form/FieldError';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { DIARY_MAX_LENGTH, DIARY_TEXT } from '@/content/diaryText';
import { describeError } from '@/content/errorMessages';
import { diaryQueryOptions, useDeleteDay, useSaveDiary } from '@/diary/queries';
import { stateValue } from '@/lib/locationState';
import { formatRecordDate, formatRecordDateShort, isRecordDate, monthOf } from '@/lib/recordDate';
import { NotFoundPage } from '@/pages/NotFoundPage';
import '@/styles/journal.css';

interface DiaryEditorProps {
  date: RecordDate;
  initialText: string;
  /** 서버에 있는 일기가 마지막으로 바뀐 시각. 일기가 없으면 null이다. */
  updatedAt: string | null;
  /** 서버의 새 글로 다시 연다. 쓰던 내용은 버려진다. */
  onReload: () => void;
  /** 확인한 일기를 고치는 중이면 그만둘 수 있다. 초안을 확인하는 중에는 그만둘 것이 없다. */
  onCancel?: () => void;
  onSaved: () => void;
}

/**
 * 일기를 고치는 자리. 저절로 저장하지 않는다. 저장을 눌러야 그 글이 그날의 일기가 된다.
 * 쓰는 도중의 글이 "확인한 일기"로 굳어지면 안 되기 때문이다.
 */
function DiaryEditor({
  date,
  initialText,
  updatedAt,
  onReload,
  onCancel,
  onSaved,
}: DiaryEditorProps) {
  const save = useSaveDiary(date);
  const [text, setText] = useState(initialText);
  // 이 자리를 열 때 보고 있던 글의 시각. 열어 둔 사이에 서버의 일기가 바뀌면(다른 기기에서 나눈 대화의 초안이 도착하면) 달라진다.
  const [openedAt] = useState(updatedAt);
  const outdated = updatedAt !== openedAt;
  const [fieldError, setFieldError] = useState<string | undefined>(undefined);
  const [summary, setSummary] = useState<string | null>(null);
  const inputId = useId();
  const errorId = useId();
  const noteId = useId();
  const inputRef = useRef<HTMLTextAreaElement>(null);

  const dirty = text !== initialText;
  const length = Array.from(text.trim()).length;

  // 저장하지 않은 글이 있는데 창을 닫거나 새로 고치려 하면 브라우저가 한 번 물어보게 한다.
  useEffect(() => {
    if (!dirty) return;
    const warn = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty]);

  function reject(message: string) {
    setFieldError(message);
    inputRef.current?.focus();
  }

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    if (save.isPending) return;
    setSummary(null);

    const trimmed = text.trim();
    if (trimmed === '') return reject(DIARY_TEXT.textRequired);
    if (length > DIARY_MAX_LENGTH) return reject(DIARY_TEXT.textTooLong);
    setFieldError(undefined);

    save.mutate(trimmed, {
      onSuccess: onSaved,
      onError: (error) => {
        if (isApiError(error) && error.fields.includes('text')) {
          reject(DIARY_TEXT.textRequired);
        } else if (isApiError(error) && error.status === 404) {
          setSummary(DIARY_TEXT.noDayToWrite);
        } else {
          setSummary(describeError(error).message);
        }
      },
    });
  }

  return (
    <form
      onSubmit={handleSubmit}
      noValidate
      aria-busy={save.isPending}
      className="journal-editor flex flex-col gap-3"
    >
      <label htmlFor={inputId} className="sr-only">
        {DIARY_TEXT.editorLabel}
      </label>
      <textarea
        id={inputId}
        ref={inputRef}
        value={text}
        onChange={(event) => setText(event.target.value)}
        rows={12}
        aria-invalid={fieldError !== undefined}
        aria-describedby={fieldError === undefined ? noteId : `${errorId} ${noteId}`}
        // 글자 크기를 16px 아래로 내리지 않는다. 더 작으면 iOS가 입력란에 초점이 갈 때 화면을 확대한다.
        className="journal-writing-paper field-sizing-content min-h-72 w-full rounded-2xl border border-input bg-card px-5 py-4 text-base leading-loose text-foreground aria-invalid:border-destructive"
      />
      <FieldError id={errorId} message={fieldError} />
      <p id={noteId} className="text-sm text-muted-foreground">
        {DIARY_TEXT.editNote}
      </p>
      {length > DIARY_MAX_LENGTH * 0.9 && (
        <p className="text-sm text-muted-foreground tabular-nums">
          {length.toLocaleString('ko-KR')} / {DIARY_MAX_LENGTH.toLocaleString('ko-KR')}
        </p>
      )}
      {summary !== null && (
        <Alert variant="destructive">
          <AlertDescription>{summary}</AlertDescription>
        </Alert>
      )}
      {outdated && (
        // 쓰던 글을 말없이 갈아 끼우지 않는다. 지금 저장하면 새로 도착한 초안이 이 글로 덮인다는 것을 알리고 고르게 한다.
        <Alert role="status">
          <AlertDescription>
            <p>{DIARY_TEXT.outdated}</p>
            <Button type="button" variant="outline" size="sm" onClick={onReload}>
              {DIARY_TEXT.reload}
            </Button>
          </AlertDescription>
        </Alert>
      )}
      <div className="flex flex-col gap-2 sm:flex-row-reverse">
        <Button type="submit" size="lg" disabled={save.isPending} className="sm:flex-1">
          {save.isPending ? DIARY_TEXT.saving : DIARY_TEXT.save}
        </Button>
        {onCancel && (
          <Button
            type="button"
            variant="outline"
            size="lg"
            onClick={onCancel}
            disabled={save.isPending}
            className="sm:flex-1"
          >
            {DIARY_TEXT.cancelEdit}
          </Button>
        )}
      </div>
    </form>
  );
}

interface DeleteDayProps {
  date: RecordDate;
}

/** 하루를 지우는 자리. 무엇이 함께 사라지는지 하나하나 적어 보여 주고 한 번 더 확인받는다. */
function DeleteDay({ date }: DeleteDayProps) {
  const remove = useDeleteDay(date);
  const navigate = useNavigate();
  const [confirming, setConfirming] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const openRef = useRef<HTMLButtonElement>(null);
  const label = formatRecordDateShort(date);

  function leave() {
    void navigate(`/diary?month=${monthOf(date)}`, {
      replace: true,
      state: { notice: DIARY_TEXT.deleted(label) },
    });
  }

  function handleConfirm() {
    setFailure(null);
    remove.mutate(undefined, {
      onSuccess: leave,
      onError: (error) => {
        // 이미 지워진 날이다(다른 기기에서 지웠거나 두 번 눌렀다). 바라던 결과와 같으므로 지운 것으로 다룬다.
        if (isApiError(error) && error.status === 404) leave();
        else setFailure(describeError(error).message);
      },
    });
  }

  function handleCancel() {
    setConfirming(false);
    setFailure(null);
    window.setTimeout(() => openRef.current?.focus(), 0);
  }

  if (!confirming) {
    return (
      <Button
        ref={openRef}
        type="button"
        variant="ghost"
        onClick={() => setConfirming(true)}
        className="self-start px-0 text-destructive hover:bg-transparent hover:underline"
      >
        {DIARY_TEXT.deleteOpen}
      </Button>
    );
  }

  return (
    <ConfirmPanel
      title={DIARY_TEXT.deleteTitle(label)}
      confirmLabel={DIARY_TEXT.deleteConfirm}
      cancelLabel={DIARY_TEXT.deleteCancel}
      onConfirm={handleConfirm}
      onCancel={handleCancel}
      destructive
      busy={remove.isPending}
    >
      <p>{DIARY_TEXT.deleteLead}</p>
      <ul className="flex list-disc flex-col gap-1 pl-5 text-foreground">
        {DIARY_TEXT.deleteItems.map((item) => (
          <li key={item}>{item}</li>
        ))}
      </ul>
      <p>{DIARY_TEXT.deleteWarning}</p>
      {failure !== null && (
        <p role="alert" className="text-destructive">
          {failure}
        </p>
      )}
    </ConfirmPanel>
  );
}

function draftIntro(diary: Diary): string {
  if (diary.text === '') return DIARY_TEXT.emptyDraftIntro;
  // 한 번 확인한 일기가 다시 초안이 됐다면 그날 새로 나눈 이야기가 이어 붙은 것이다.
  return diary.confirmed_at === null ? DIARY_TEXT.draftIntro : DIARY_TEXT.draftAppendedIntro;
}

function DiaryForDate({ date }: { date: RecordDate }) {
  const diary = useQuery(diaryQueryOptions(date));
  const location = useLocation();
  // null이면 일기의 상태를 따른다. 초안은 고치는 화면으로, 확인한 일기는 읽는 화면으로 연다.
  const [editing, setEditing] = useState<boolean | null>(null);
  const [justSaved, setJustSaved] = useState(false);
  // 값을 올리면 고치는 자리를 서버의 지금 글로 새로 연다.
  const [editorVersion, setEditorVersion] = useState(0);
  const statusRef = useRef<HTMLParagraphElement>(null);

  const fromTalk = stateValue(location.state, 'fromTalk') === true;

  const data = diary.data;
  const isEditing = editing ?? data?.status === 'draft';

  function startEditing() {
    setJustSaved(false);
    setEditorVersion((current) => current + 1);
    setEditing(true);
  }

  function handleSaved() {
    setEditing(false);
    setJustSaved(true);
    // 저장 버튼이 사라진다. 저장됐다는 안내로 초점을 옮겨 화면 낭독기가 결과를 읽게 한다.
    window.setTimeout(() => statusRef.current?.focus(), 0);
  }

  return (
    <div className="journal-page journal-detail-page">
      <title>{`${formatRecordDateShort(date)}의 일기 · 내일`}</title>
      <header className="journal-detail-heading">
        <p className="text-sm text-muted-foreground">
          <Link to={`/diary?month=${monthOf(date)}`} className="journal-back-link">
            <ArrowLeftIcon aria-hidden="true" />
            {DIARY_TEXT.backToList}
          </Link>
        </p>
        <p className="journal-eyebrow">{DIARY_TEXT.eyebrow}</p>
        <h1>
          <time dateTime={date}>{formatRecordDate(date)}</time>
        </h1>
      </header>

      {diary.isPending && (
        <p role="status" className="animate-appear-late text-muted-foreground">
          {DIARY_TEXT.loading}
        </p>
      )}

      {diary.isError && data === undefined && (
        <Alert variant="destructive">
          <AlertDescription>
            <p>{describeError(diary.error).message}</p>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={diary.isFetching}
              onClick={() => void diary.refetch()}
            >
              {DIARY_TEXT.retry}
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {justSaved && (
        <p
          ref={statusRef}
          role="status"
          tabIndex={-1}
          className="rounded-xl border bg-card px-5 py-4 outline-none"
        >
          {DIARY_TEXT.saved}
        </p>
      )}

      {data === null && !isEditing && (
        <div className="journal-empty flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <h2 className="text-lg font-semibold">{DIARY_TEXT.noDiaryTitle}</h2>
            <p className="text-muted-foreground">{DIARY_TEXT.noDiaryBody}</p>
          </div>
          <Button type="button" variant="outline" onClick={startEditing}>
            {DIARY_TEXT.writeOwn}
          </Button>
        </div>
      )}

      {data !== undefined && isEditing && (
        <div className="flex flex-col gap-3">
          {data?.status === 'draft' && (
            <div className="flex flex-col gap-1">
              {fromTalk && (
                <p role="status" className="font-semibold">
                  {DIARY_TEXT.draftReadyFromTalk}
                </p>
              )}
              <p className="text-muted-foreground">{draftIntro(data)}</p>
            </div>
          )}
          <DiaryEditor
            // 처음의 글은 열 때 한 번만 읽는다. 뒤에서 일기를 다시 받아 와도 쓰던 내용을 건드리지 않는다.
            key={editorVersion}
            date={date}
            initialText={data?.text ?? ''}
            updatedAt={data?.updated_at ?? null}
            onReload={startEditing}
            onCancel={data?.status === 'draft' ? undefined : () => setEditing(null)}
            onSaved={handleSaved}
          />
        </div>
      )}

      {data != null && !isEditing && (
        <article className="journal-article">
          <div className="journal-paper">
            <span className="journal-paper-label">
              <CheckIcon aria-hidden="true" />
              {DIARY_TEXT.confirmedAt}
            </span>
            <p className="journal-paper-text">{data.text}</p>
          </div>
          <Button type="button" variant="outline" onClick={startEditing}>
            <PenLineIcon aria-hidden="true" />
            {DIARY_TEXT.edit}
          </Button>
        </article>
      )}

      {/* 일기가 없는 날에도 그날의 대화는 있을 수 있다. 지우는 길은 늘 열어 둔다. */}
      {data !== undefined && (
        <div className="flex flex-col border-t pt-4">
          <DeleteDay date={date} />
        </div>
      )}
    </div>
  );
}

/** 하루의 일기. 초안이면 확인하고 고쳐서 저장하고, 확인한 일기면 읽거나 고친다. */
export function DiaryPage() {
  const { date } = useParams();

  // 주소의 값은 꼴을 믿지 않는다. 달력에 없는 날짜로는 서버에 묻지도 않는다.
  if (date === undefined || !isRecordDate(date)) return <NotFoundPage />;
  return <DiaryForDate key={date} date={date} />;
}
