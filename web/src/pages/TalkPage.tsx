import {
  AudioLinesIcon,
  ChevronLeftIcon,
  KeyboardIcon,
  LifeBuoyIcon,
  MessageSquareTextIcon,
  MicIcon,
  MicOffIcon,
  XIcon,
} from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { Link, NavLink } from 'react-router';

import { ConfirmPanel } from '@/components/ConfirmPanel';
import { ResourceList } from '@/components/ResourceList';
import { Caption } from '@/components/talk/Caption';
import { Composer } from '@/components/talk/Composer';
import { EndedPanel } from '@/components/talk/EndedPanel';
import { MessageList } from '@/components/talk/MessageList';
import { Orb, type OrbAction, type OrbMode } from '@/components/talk/Orb';
import { Button } from '@/components/ui/button';
import { noticeText, phaseText, TALK_TEXT } from '@/content/talkText';
import { formatRecordDateShort } from '@/lib/recordDate';
import { cn } from '@/lib/utils';
import type { ConversationState } from '@/talk/conversationState';
import { useConversation } from '@/talk/useConversation';
import '@/styles/talk.css';

/**
 * 소리 없이 글로만 답이 왔을 때 구슬이 "말하는" 모양으로 있는 시간. 읽는 데 걸리는 만큼이다.
 * 소리가 붙은 말은 재생이 끝나는 순간이 이 자리를 맡는다.
 */
function speakingMillis(text: string): number {
  return Math.min(6_000, 1_200 + text.length * 80);
}

/**
 * 방금 도착한 말을 글로 건네는 동안 참이다. 말마다 한 번씩 켜졌다가 읽을 만큼의 시간이 지나면 꺼진다.
 * 그 말에 소리가 붙었으면(voicedSeq) 시간으로 흉내 내지 않는다. 소리가 끝난 뒤에 다시 말하는 모양이 되면 어색하다.
 */
function useTimedSpeaking(
  arrived: ConversationState['arrived'],
  voicedSeq: ConversationState['voicedSeq'],
): boolean {
  const [doneSeq, setDoneSeq] = useState<number | null>(null);

  useEffect(() => {
    if (arrived === null) return;
    const timer = window.setTimeout(() => setDoneSeq(arrived.seq), speakingMillis(arrived.text));
    return () => window.clearTimeout(timer);
  }, [arrived]);

  return arrived !== null && arrived.seq !== voicedSeq && doneSeq !== arrived.seq;
}

function orbModeOf(state: ConversationState, timedSpeaking: boolean): OrbMode {
  if (state.phase === 'failed' || state.phase === 'taken_over' || state.phase === 'ended') {
    return 'off';
  }
  // 처음 잇는 중이거나 다시 잇는 중이면 조용히 숨만 쉰다.
  if (state.phase !== 'ready') return 'idle';
  if (state.speaking !== null) return 'speaking';
  if (state.awaitingReply) return 'thinking';
  if (timedSpeaking) return 'speaking';
  if (state.mic === 'on' && state.mode === 'voice' && state.listening) return 'listening';
  return 'idle';
}

interface TalkSessionProps {
  onTalkAgain: () => void;
}

function TalkSession({ onTalkAgain }: TalkSessionProps) {
  const talk = useConversation();
  const { state } = talk;
  const [confirmingEnd, setConfirmingEnd] = useState(false);
  const [transcriptOpen, setTranscriptOpen] = useState(false);
  /** 음성으로 이야기하는 중에 글 쓰는 자리를 열어 두었는지 */
  const [keyboardOpen, setKeyboardOpen] = useState(false);
  /** 구슬을 눌러 시작하라는 권유를 거둘 때. 한 번 시작했거나, 글로 먼저 이야기했거나, 음성을 껐으면 다시 권하지 않는다. */
  const [offerDismissed, setOfferDismissed] = useState(false);
  const endButtonRef = useRef<HTMLButtonElement>(null);
  const orbRef = useRef<HTMLElement>(null);
  const timedSpeaking = useTimedSpeaking(state.arrived, state.voicedSeq);

  // 소리 크기는 초당 수십 번 온다. 상태로 두면 화면 전체가 그만큼 다시 그려지므로 구슬의 CSS 변수에 바로 넣는다.
  const { subscribeLevel } = talk;
  useEffect(
    () =>
      subscribeLevel((level) => {
        orbRef.current?.style.setProperty('--orb-level', level.toFixed(3));
      }),
    [subscribeLevel],
  );

  const ended = state.ended;
  const canSend = state.phase === 'ready' && !state.awaitingReply && !state.ending;
  const orbMode = orbModeOf(state, timedSpeaking);
  /** 마이크가 켜져 있거나 켜지는 중. 맨 아래 줄이 음성 모양이 된다. */
  const voiceOn = state.mic === 'on' || state.mic === 'starting';
  /** 마이크가 켜져 있고 서버도 음성으로 열려 있다. 듣고 있거나, 한 마디가 끝나 다음 누름을 기다린다. */
  const voiceReady = state.mic === 'on' && state.mode === 'voice';
  const listeningNow = voiceReady && state.listening;
  const offerVoice =
    !offerDismissed &&
    state.voiceAvailable &&
    state.phase === 'ready' &&
    state.mic === 'off' &&
    !state.ending &&
    talk.preferredMode === 'voice';
  // 상태 줄에는 한 번에 한 가지만 적는다. 마치는 중 > 연결 상태 > 답을 준비하는 중.
  const statusLine = state.ending
    ? TALK_TEXT.ending
    : (phaseText(state.phase) ?? (state.awaitingReply ? TALK_TEXT.thinking : null));

  // 구슬은 때에 따라 다른 일을 한다. 시작 전에는 시작, 내일이 말하는 동안은 끊고 말하기,
  // 듣는 동안은 말 끝맺기, 한 마디가 끝나 멈춰 있으면 다음 말 듣기.
  const orbAction: OrbAction | null = offerVoice
    ? { label: TALK_TEXT.orbStart, onPress: startVoice }
    : state.speaking !== null
      ? { label: TALK_TEXT.orbInterrupt, onPress: talk.interrupt }
      : listeningNow && !state.awaitingReply && !state.ending
        ? { label: TALK_TEXT.orbDone, onPress: talk.finalize }
        : voiceReady && !state.listening && !state.awaitingReply && !state.ending
          ? { label: TALK_TEXT.orbListen, onPress: talk.listen }
          : null;

  // 사용자 동작 처리기에서 바로 부른다. iOS는 그 안에서 연 오디오만 소리를 낸다.
  function startVoice() {
    setOfferDismissed(true);
    talk.startVoice();
  }

  function stopVoice() {
    setOfferDismissed(true);
    setKeyboardOpen(false);
    talk.stopVoice();
  }

  function send(text: string): boolean {
    const sent = talk.send(text);
    if (sent) setOfferDismissed(true);
    return sent;
  }

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
    <div className="talk-page" data-has-resources={state.resources !== null}>
      <title>이야기하기 · 내일</title>

      {/* 앱의 머리말은 이 화면에서 접힌다. 처음으로 돌아가는 길과 "도움이 필요할 때"는 여기서 같은 자리에 둔다. */}
      <header className="talk-header">
        <Link to="/" aria-label={TALK_TEXT.back} className="talk-header__back">
          <ChevronLeftIcon aria-hidden="true" className="size-6" />
        </Link>
        <div className="talk-header__title">
          <h1>{TALK_TEXT.title}</h1>
          {state.recordDate !== null && (
            <p>
              <time dateTime={state.recordDate}>{formatRecordDateShort(state.recordDate)}</time>
            </p>
          )}
        </div>
        {/* 힘든 순간은 어느 화면에서든 올 수 있다. 대화 중에도 이 링크는 다른 화면과 같은 자리(오른쪽 위)에 있다. */}
        <NavLink
          to="/help"
          className={({ isActive }) =>
            cn('talk-header__help', isActive && 'font-semibold text-foreground')
          }
        >
          <LifeBuoyIcon aria-hidden="true" className="size-4" />
          {TALK_TEXT.help}
        </NavLink>
      </header>

      {state.resources !== null && (
        <section
          aria-labelledby="talk-resources-title"
          className="talk-resources flex flex-col gap-2"
        >
          <h2 id="talk-resources-title" className="text-sm font-semibold">
            {TALK_TEXT.resourcesTitle}
          </h2>
          <ResourceList resources={state.resources} compact />
        </section>
      )}

      {ended === null && (
        <>
          {/* 연결 상태와 잠깐의 알림. 나타날 때 화면 낭독기가 읽어 준다. */}
          <div role="status" className="talk-status">
            {statusLine !== null && <p>{statusLine}</p>}
            {state.notice !== null && <p>{noticeText(state.notice)}</p>}
          </div>
          {(state.phase === 'failed' || state.phase === 'taken_over') && (
            <Button
              type="button"
              variant="outline"
              onClick={talk.reconnect}
              className="mt-2 self-center"
            >
              {state.phase === 'taken_over' ? TALK_TEXT.takeOver : TALK_TEXT.reconnect}
            </Button>
          )}
        </>
      )}

      {ended !== null ? (
        <div className="talk-ended">
          <Orb mode="off" className="talk-ended__orb" />
          <EndedPanel ended={ended} diaryReady={state.diaryReady} onTalkAgain={onTalkAgain} />
        </div>
      ) : transcriptOpen ? (
        <div className="talk-transcript">
          <MessageList
            messages={state.messages}
            thinking={state.awaitingReply}
            canRetry={canSend}
            onRetry={talk.retry}
          />
        </div>
      ) : (
        <div className="talk-stage">
          <div className="talk-stage__orb">
            <Orb ref={orbRef} mode={orbMode} action={orbAction} />
          </div>
          <Caption
            messages={state.messages}
            partial={state.partial}
            thinking={state.awaitingReply}
            canRetry={canSend}
            onRetry={talk.retry}
          />
          {offerVoice && <p className="talk-stage__hint">{TALK_TEXT.orbStartHint}</p>}
        </div>
      )}

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

      {ended === null && (
        <div className="talk-controls">
          {confirmingEnd && (
            <ConfirmPanel
              title={TALK_TEXT.endConfirmTitle}
              confirmLabel={TALK_TEXT.endConfirm}
              cancelLabel={TALK_TEXT.endCancel}
              onConfirm={confirmEnd}
              onCancel={cancelEnd}
            >
              <p>{TALK_TEXT.endConfirmBody}</p>
            </ConfirmPanel>
          )}
          {/* 입력란은 독립된 한 줄을 써서 작은 화면에서도 충분한 너비를 확보한다. */}
          {voiceOn ? (
            <>
              {keyboardOpen && <Composer canSend={canSend} onSend={send} />}
              <div className="talk-voice-status" data-listening={listeningNow}>
                <span className="talk-wave" aria-hidden="true">
                  {[0, 1, 2, 3, 4].map((bar) => (
                    <span key={bar} />
                  ))}
                </span>
                <AudioLinesIcon aria-hidden="true" className="sr-only" />
                <span>
                  {listeningNow
                    ? TALK_TEXT.listening
                    : voiceReady
                      ? TALK_TEXT.pausedHint
                      : TALK_TEXT.micStarting}
                </span>
              </div>
            </>
          ) : (
            <div className="talk-controls__input-row">
              <Composer canSend={canSend} onSend={send} className="flex-1" />
              {state.voiceAvailable && (
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  aria-label={TALK_TEXT.voiceStart}
                  onClick={startVoice}
                  disabled={state.ending}
                  className="talk-control talk-control--mic"
                >
                  <MicIcon aria-hidden="true" />
                </Button>
              )}
            </div>
          )}
          <div className="talk-controls__toolbar">
            <Button
              type="button"
              variant="ghost"
              aria-label={TALK_TEXT.logLabel}
              aria-pressed={transcriptOpen}
              onClick={() => setTranscriptOpen((open) => !open)}
              className="talk-control talk-control--label"
            >
              <MessageSquareTextIcon aria-hidden="true" />
              <span>{TALK_TEXT.logLabel}</span>
            </Button>
            {voiceOn && (
              <div className="talk-controls__voice-actions">
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  aria-label={TALK_TEXT.typeInstead}
                  aria-pressed={keyboardOpen}
                  onClick={() => setKeyboardOpen((open) => !open)}
                  className="talk-control"
                >
                  <KeyboardIcon aria-hidden="true" />
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  aria-label={TALK_TEXT.voiceStop}
                  onClick={stopVoice}
                  className="talk-control"
                >
                  <MicOffIcon aria-hidden="true" />
                </Button>
              </div>
            )}
            {/* 끝내기는 언제나 보인다. 연결이 끊겨 있어도 누를 수 있고, 다시 이어지는 대로 서버에 전한다. */}
            <Button
              ref={endButtonRef}
              type="button"
              variant="ghost"
              aria-label={TALK_TEXT.end}
              onClick={() => setConfirmingEnd(true)}
              disabled={state.ending || confirmingEnd}
              className="talk-control talk-control--label talk-control--end"
            >
              <XIcon aria-hidden="true" />
              <span>{TALK_TEXT.end}</span>
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

/** 대화 화면. "다시 이야기하기"를 누르면 연결과 상태를 통째로 새로 만든다. */
export function TalkPage() {
  const [session, setSession] = useState(0);

  return <TalkSession key={session} onTalkAgain={() => setSession((current) => current + 1)} />;
}
