import { useRef, useState } from 'react';

import { ConfirmPanel } from '@/components/ConfirmPanel';
import { ResourceList } from '@/components/ResourceList';
import { Composer } from '@/components/talk/Composer';
import { EndedPanel } from '@/components/talk/EndedPanel';
import { MessageList } from '@/components/talk/MessageList';
import { Button } from '@/components/ui/button';
import { noticeText, phaseText, TALK_TEXT } from '@/content/talkText';
import { formatRecordDateShort } from '@/lib/recordDate';
import { useConversation } from '@/talk/useConversation';

interface TalkSessionProps {
  onTalkAgain: () => void;
}

function TalkSession({ onTalkAgain }: TalkSessionProps) {
  const talk = useConversation();
  const { state } = talk;
  const [confirmingEnd, setConfirmingEnd] = useState(false);
  const endButtonRef = useRef<HTMLButtonElement>(null);

  const ended = state.ended;
  const connectionText = phaseText(state.phase);
  const canSend = state.phase === 'ready' && !state.awaitingReply && !state.ending;

  function cancelEnd() {
    setConfirmingEnd(false);
    // 확인하는 자리가 사라진다. 초점을 그 자리를 연 버튼으로 돌려놓는다.
    window.setTimeout(() => endButtonRef.current?.focus(), 0);
  }

  function confirmEnd() {
    setConfirmingEnd(false);
    talk.end();
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <title>이야기하기 · 내일</title>

      <div className="flex items-center justify-between gap-3 pb-2">
        <div className="flex min-w-0 flex-col">
          <h1 className="text-lg leading-snug font-semibold">{TALK_TEXT.title}</h1>
          {state.recordDate !== null && (
            <p className="text-sm text-muted-foreground">
              <time dateTime={state.recordDate}>{formatRecordDateShort(state.recordDate)}</time>
            </p>
          )}
        </div>
        {/* 끝내기는 언제나 보인다. 연결이 끊겨 있어도 누를 수 있고, 다시 이어지는 대로 서버에 전한다. */}
        {ended === null && (
          <Button
            ref={endButtonRef}
            type="button"
            variant="outline"
            onClick={() => setConfirmingEnd(true)}
            disabled={state.ending || confirmingEnd}
            className="shrink-0 px-4"
          >
            {TALK_TEXT.end}
          </Button>
        )}
      </div>

      {confirmingEnd && (
        <ConfirmPanel
          title={TALK_TEXT.endConfirmTitle}
          confirmLabel={TALK_TEXT.endConfirm}
          cancelLabel={TALK_TEXT.endCancel}
          onConfirm={confirmEnd}
          onCancel={cancelEnd}
          className="mb-2"
        >
          <p>{TALK_TEXT.endConfirmBody}</p>
        </ConfirmPanel>
      )}

      {state.resources !== null && (
        <section aria-labelledby="talk-resources-title" className="flex flex-col gap-2 pb-2">
          <h2 id="talk-resources-title" className="text-sm font-semibold">
            {TALK_TEXT.resourcesTitle}
          </h2>
          <ResourceList resources={state.resources} compact />
        </section>
      )}

      {/* 연결 상태와 잠깐의 알림. 나타날 때 화면 낭독기가 읽어 준다. */}
      <div role="status" className="flex flex-col gap-1 text-sm text-muted-foreground">
        {state.ending && <p>{TALK_TEXT.ending}</p>}
        {!state.ending && connectionText !== null && <p>{connectionText}</p>}
        {state.notice !== null && <p>{noticeText(state.notice)}</p>}
      </div>
      {(state.phase === 'failed' || state.phase === 'taken_over') && (
        <Button
          type="button"
          variant="outline"
          onClick={talk.reconnect}
          className="mt-2 self-start"
        >
          {state.phase === 'taken_over' ? TALK_TEXT.takeOver : TALK_TEXT.reconnect}
        </Button>
      )}

      <div className="-mx-2 min-h-0 flex-1 overflow-y-auto overscroll-contain px-2">
        <MessageList
          messages={state.messages}
          thinking={state.awaitingReply}
          canRetry={canSend}
          onRetry={talk.retry}
        />
      </div>

      {/* 방금 도착한 말만 읽어 준다. 같은 말이 이어져도 다시 읽히도록 순번을 키로 써서 새로 그린다. */}
      <div aria-live="polite" className="sr-only">
        {state.arrived !== null && (
          <p key={state.arrived.seq}>
            {TALK_TEXT.speakerAi}: {state.arrived.text}
          </p>
        )}
        {/* 번호가 화면에 고정되는 순간을 한 번 알린다. 초점은 옮기지 않는다. 쓰던 글이 끊기면 안 된다. */}
        {state.resources !== null && <p>{TALK_TEXT.resourcesAnnounce}</p>}
      </div>

      {ended === null ? (
        <Composer canSend={canSend} onSend={talk.send} />
      ) : (
        <EndedPanel ended={ended} diaryReady={state.diaryReady} onTalkAgain={onTalkAgain} />
      )}
    </div>
  );
}

/** 대화 화면(채팅). "다시 이야기하기"를 누르면 연결과 상태를 통째로 새로 만든다. */
export function TalkPage() {
  const [session, setSession] = useState(0);

  return <TalkSession key={session} onTalkAgain={() => setSession((current) => current + 1)} />;
}
