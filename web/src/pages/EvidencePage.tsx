import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { Link, useParams } from 'react-router';

import { isApiError } from '@/api/errors';
import type { DaySignalItem, RecordDate, SignalEvidence, SignalStatus } from '@/api/types';
import { useMe } from '@/auth/session';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { describeError } from '@/content/errorMessages';
import {
  EVIDENCE_TEXT,
  explicitnessLabel,
  itemLabel,
  judgementLabel,
} from '@/content/evidenceText';
import { diaryQueryOptions } from '@/diary/queries';
import {
  ANALYSIS_GRACE_MS,
  daySignalsQueryOptions,
  POLL_INTERVAL_MS,
  useSetSignalCancelled,
} from '@/evidence/queries';
import {
  formatRecordDate,
  formatRecordDateShort,
  isRecordDate,
  recordDateOf,
} from '@/lib/recordDate';
import { useNow } from '@/lib/useNow';
import { useOnline } from '@/lib/useOnline';
import { cn } from '@/lib/utils';
import { NotFoundPage } from '@/pages/NotFoundPage';

// 추세 화면의 점과 같은 말을 쓴다. 찬 점은 관찰됨, 빈 점은 이야기가 나왔고 괜찮았음, 작은 점은 말하지 않음이다.
const DOT: Record<SignalStatus, string> = {
  observed: 'size-3 bg-primary',
  not_observed: 'size-3 border-2 border-input',
  not_mentioned: 'size-1.5 bg-input',
};

function StatusDot({ status }: { status: SignalStatus }) {
  return (
    // 옆의 글이 같은 것을 말하므로 화면 낭독기에는 알리지 않는다.
    <span aria-hidden="true" className="mt-2 flex size-3 shrink-0 items-center justify-center">
      <span className={cn('rounded-full', DOT[status])} />
    </span>
  );
}

function Badge({ children }: { children: string }) {
  return (
    <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
      {children}
    </span>
  );
}

interface EvidenceRowProps {
  date: RecordDate;
  item: DaySignalItem;
  row: SignalEvidence;
  /** 그 항목의 한 줄과 이 행의 판단이 다를 때만 행에도 판단을 적는다(하루에 여러 번 대화했을 때). */
  showJudgement: boolean;
}

/**
 * 판단 하나와 그 근거가 된 사용자의 말.
 *
 * 근거는 손대지 않고 그대로 보여 준다. 줄이거나 다듬으면 "내가 한 말"이 아니게 되고,
 * 이 화면이 지키는 약속("왜 그렇게 봤는지 직접 확인할 수 있다")이 깨진다.
 */
function EvidenceRow({ date, item, row, showJudgement }: EvidenceRowProps) {
  const change = useSetSignalCancelled(date);
  const [failure, setFailure] = useState<string | null>(null);
  const label = itemLabel(item.item);
  const explicitness = explicitnessLabel(row.explicitness);

  function toggle() {
    setFailure(null);
    change.mutate(
      { signalId: row.id, cancelled: !row.cancelled },
      {
        onError: (error) => {
          // 이미 없는 행이면 다시 눌러도 같은 답이 온다. 다시 눌러 보라고 하지 않는다.
          const gone = isApiError(error) && error.status === 404;
          setFailure(gone ? EVIDENCE_TEXT.cancelGone : EVIDENCE_TEXT.cancelFailed);
        },
      },
    );
  }

  return (
    <li className="flex flex-col gap-2">
      {row.evidence !== null && (
        <blockquote
          className={cn(
            'border-l-2 border-border pl-4 leading-loose',
            row.cancelled && 'text-muted-foreground',
          )}
        >
          {/* q는 브라우저가 따옴표를 붙여 준다. 글자를 더하지 않으므로 인용한 말이 그대로 남는다. */}
          <q>{row.evidence}</q>
        </blockquote>
      )}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        {showJudgement && (
          <span className="text-sm text-muted-foreground">
            {judgementLabel(item.item, row.status)}
          </span>
        )}
        {explicitness !== null && <Badge>{explicitness}</Badge>}
        {row.cancelled && <Badge>{EVIDENCE_TEXT.cancelledBadge}</Badge>}
        {/*
          이 단추는 "AI가 잘못 읽은 것을 내가 되돌린다"의 손잡이다. 옆의 알약과 같은 회색 글자로 두면
          누를 수 있는 것으로 보이지 않는다. 테두리를 두르고 알약과 사이를 벌려 따로 읽히게 한다.
        */}
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={toggle}
          aria-label={row.cancelled ? EVIDENCE_TEXT.undoFor(label) : EVIDENCE_TEXT.cancelFor(label)}
          className="ml-auto"
        >
          {row.cancelled ? EVIDENCE_TEXT.undo : EVIDENCE_TEXT.cancel}
        </Button>
      </div>
      {row.cancelled && (
        // 눌린 뒤에 나타나므로 화면 낭독기가 무엇이 달라졌는지 읽어 준다.
        <p role="status" className="text-sm text-muted-foreground">
          {EVIDENCE_TEXT.cancelledNote}
        </p>
      )}
      {failure !== null && (
        <p role="alert" className="text-sm text-destructive">
          {failure}
        </p>
      )}
    </li>
  );
}

/** 취소한 판단만 남은 항목은 "말하지 않음"이 아니다. 이야기는 나왔고 사용자가 아니라고 한 것이다. */
function headerJudgement(item: DaySignalItem): string {
  const withdrawn = item.rows.some((row) => row.cancelled && row.status !== 'not_mentioned');
  if (item.status === 'not_mentioned' && withdrawn) return EVIDENCE_TEXT.cancelledBadge;
  return judgementLabel(item.item, item.status);
}

function ItemCard({ date, item }: { date: RecordDate; item: DaySignalItem }) {
  // 이야기가 없었던 항목은 보여 줄 근거도, 아니라고 할 판단도 없다.
  const rows = item.rows.filter((row) => row.status !== 'not_mentioned');
  const merged = headerJudgement(item);

  return (
    <li className="flex flex-col gap-3 rounded-2xl border bg-card px-5 py-4">
      <div className="flex items-start gap-3">
        <StatusDot status={item.status} />
        <div className="flex flex-col gap-0.5">
          <h2 className="text-base leading-snug font-semibold">{itemLabel(item.item)}</h2>
          <p className="leading-snug text-muted-foreground">{merged}</p>
        </div>
      </div>
      {rows.length > 0 && (
        <ul className="flex flex-col gap-4 pl-6">
          {rows.map((row) => (
            <EvidenceRow
              key={row.id}
              date={date}
              item={item}
              row={row}
              showJudgement={judgementLabel(item.item, row.status) !== merged}
            />
          ))}
        </ul>
      )}
    </li>
  );
}

interface NotReadyProps {
  title: string;
  body: string;
  /** 한 줄로 다 담기지 않을 때만 덧붙인다. */
  hint?: string;
  onRefresh?: () => void;
  refreshing?: boolean;
}

/** 아직 볼 것이 없는 날. 어떤 까닭이든 조용한 한 줄로 말하고, 끝없이 도는 표시를 남기지 않는다. */
function NotReady({ title, body, hint, onRefresh, refreshing = false }: NotReadyProps) {
  return (
    <div className="flex flex-col gap-3 rounded-2xl border bg-card px-5 py-6">
      <h2 className="text-lg leading-snug font-semibold">{title}</h2>
      <p className="text-muted-foreground">{body}</p>
      {hint !== undefined && <p className="text-muted-foreground">{hint}</p>}
      {onRefresh && (
        <Button
          type="button"
          variant="outline"
          onClick={onRefresh}
          disabled={refreshing}
          className="self-start"
        >
          {refreshing ? EVIDENCE_TEXT.refreshing : EVIDENCE_TEXT.refresh}
        </Button>
      )}
    </div>
  );
}

function millisSince(now: Date, timestamp: string): number {
  const at = Date.parse(timestamp);
  // 읽을 수 없는 시각이면 "오래 기다렸다"로 본다. 끝을 모르는 기다림을 만들지 않는다.
  return Number.isNaN(at) ? Number.POSITIVE_INFINITY : now.getTime() - at;
}

function EvidenceForDate({ date }: { date: RecordDate }) {
  const me = useMe();
  const now = useNow();
  const online = useOnline();
  // 그날 이야기를 나눴는지는 그날의 일기로 안다. 대화가 끝나면 초안이 자동으로 남는다.
  // 일기 화면과 같은 조회를 써서, 두 화면이 그날을 두고 다른 말을 하지 않는다.
  const diary = useQuery(diaryQueryOptions(date));

  const user = me.data?.user;
  const analysisEnabled = me.data?.settings.analysis_enabled !== false;
  const talked = diary.data != null;
  const waitedTooLong =
    diary.data == null || millisSince(now, diary.data.updated_at) > ANALYSIS_GRACE_MS;
  // 기다릴 까닭이 있을 때만 저절로 다시 물어본다. 연결이 끊긴 동안에는 물어봐도 같은 실패만 쌓인다.
  const canWait = analysisEnabled && talked && !waitedTooLong && online;

  const signals = useQuery({
    ...daySignalsQueryOptions(date),
    refetchInterval: (query) =>
      query.state.data?.analysed === false && canWait ? POLL_INTERVAL_MS : false,
  });

  const day = signals.data;
  const isToday = user !== undefined && recordDateOf(now, user.timezone) === date;
  const refreshing = signals.isFetching || diary.isFetching;

  function refresh() {
    void signals.refetch();
    void diary.refetch();
  }

  function notReady(): NotReadyProps {
    if (!analysisEnabled) {
      return { title: EVIDENCE_TEXT.disabledTitle, body: EVIDENCE_TEXT.disabledBody };
    }
    // 그날 이야기를 나눴는지 아직 모른다(일기 조회가 실패했다). 스스로 기다리지 않고 사람에게 넘긴다.
    if (diary.data === undefined) {
      return {
        title: EVIDENCE_TEXT.stalledTitle,
        body: EVIDENCE_TEXT.stalledBody,
        onRefresh: refresh,
        refreshing,
      };
    }
    if (!talked) {
      // 오늘이면 방금 마친 이야기가 아직 일기로 남지 않았을 수 있다. 그때는 다시 볼 길을 함께 둔다.
      return {
        title: EVIDENCE_TEXT.noConversationTitle,
        body: EVIDENCE_TEXT.noConversationBody,
        hint: isToday ? EVIDENCE_TEXT.justTalkedHint : undefined,
        onRefresh: isToday ? refresh : undefined,
        refreshing,
      };
    }
    if (waitedTooLong) {
      return {
        title: EVIDENCE_TEXT.stalledTitle,
        body: EVIDENCE_TEXT.stalledBody,
        onRefresh: refresh,
        refreshing,
      };
    }
    return { title: EVIDENCE_TEXT.pendingTitle, body: EVIDENCE_TEXT.pendingBody };
  }

  const loading = signals.isPending || (day?.analysed === false && diary.isPending);

  return (
    <div className="flex flex-col gap-6">
      <title>{EVIDENCE_TEXT.pageTitle(formatRecordDateShort(date))}</title>
      <div className="flex flex-col gap-1">
        <p className="text-sm text-muted-foreground">
          <Link to="/trend" className="underline underline-offset-4">
            {EVIDENCE_TEXT.backToTrend}
          </Link>
        </p>
        <h1 className="text-2xl leading-snug font-semibold">
          <time dateTime={date}>{formatRecordDate(date)}</time>
        </h1>
      </div>

      {loading && (
        <p role="status" className="animate-appear-late text-muted-foreground">
          {EVIDENCE_TEXT.loading}
        </p>
      )}

      {signals.isError && day === undefined && (
        <Alert variant="destructive">
          <AlertDescription>
            <p>{describeError(signals.error).message}</p>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={signals.isFetching}
              onClick={() => void signals.refetch()}
            >
              {EVIDENCE_TEXT.retry}
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {day?.analysed === true && (
        <div className="flex flex-col gap-4">
          <p className="text-muted-foreground">{EVIDENCE_TEXT.lead}</p>
          <ul aria-label={EVIDENCE_TEXT.itemsLabel} className="flex flex-col gap-3">
            {day.items.map((item) => (
              <ItemCard key={item.item} date={date} item={item} />
            ))}
          </ul>
          <p className="text-sm text-muted-foreground">{EVIDENCE_TEXT.cancelNote}</p>
        </div>
      )}

      {day?.analysed === false && !loading && <NotReady {...notReady()} />}

      {/* 갈 곳이 없으면 줄도 긋지 않는다. */}
      {(talked || isToday) && (
        <div className="flex flex-col items-start gap-2 border-t pt-4">
          {talked ? (
            <Link to={`/diary/${date}`} className="underline underline-offset-4">
              {EVIDENCE_TEXT.toDiary}
            </Link>
          ) : (
            <Link to="/talk" className="underline underline-offset-4">
              {EVIDENCE_TEXT.toTalk}
            </Link>
          )}
        </div>
      )}
    </div>
  );
}

/** 하루의 마음 신호와, 그렇게 본 근거가 된 내 말. 신호 하나하나를 아니라고 표시할 수 있다. */
export function EvidencePage() {
  const { date } = useParams();

  // 주소의 값은 꼴을 믿지 않는다. 달력에 없는 날짜로는 서버에 묻지도 않는다.
  if (date === undefined || !isRecordDate(date)) return <NotFoundPage />;
  return <EvidenceForDate key={date} date={date} />;
}
