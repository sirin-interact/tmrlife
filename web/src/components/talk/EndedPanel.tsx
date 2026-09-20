import { useEffect, useRef } from 'react';
import { Link, useNavigate } from 'react-router';

import { Button } from '@/components/ui/button';
import { endedText, TALK_TEXT } from '@/content/talkText';
import { useDraftArrival } from '@/diary/useDraftArrival';
import type { EndedInfo } from '@/talk/conversationState';

interface EndedPanelProps {
  ended: EndedInfo;
  /** 대화 채널이 일기 초안이 준비됐다고 알려 왔는지 */
  diaryReady: boolean;
  onTalkAgain: () => void;
}

/** 대화가 끝난 뒤의 자리. 일기 초안을 기다렸다가, 준비되면 일기 확인 화면으로 넘어간다. */
export function EndedPanel({ ended, diaryReady, onTalkAgain }: EndedPanelProps) {
  const navigate = useNavigate();
  const titleRef = useRef<HTMLHeadingElement>(null);

  // 서버의 답을 듣지 못하고 끝났으면(null) 초안을 만들고 있을 수 있다. 기다려 본다.
  // 이 화면이 모르는 사이에 끝나 있던 대화는 기다리지 않는다. 일기가 이미 만들어져 있어서 달라질 것이 없다.
  const waiting = ended.diaryExpected !== false && ended.reason !== 'elsewhere';
  const polled = useDraftArrival(ended.recordDate, waiting && !diaryReady);
  const arrived = waiting && (diaryReady || polled.arrived);

  useEffect(() => {
    // 입력란이 사라진다. 초점이 갈 곳을 잃지 않게 이 자리의 제목으로 옮긴다.
    titleRef.current?.focus();
  }, []);

  useEffect(() => {
    if (!arrived || ended.recordDate === null) return;
    void navigate(`/diary/${ended.recordDate}`, { replace: true, state: { fromTalk: true } });
  }, [arrived, ended.recordDate, navigate]);

  return (
    <section
      aria-labelledby="talk-ended-title"
      className="flex flex-col gap-4 border-t bg-background pt-4 pb-[max(1rem,env(safe-area-inset-bottom))]"
    >
      <h2
        id="talk-ended-title"
        ref={titleRef}
        tabIndex={-1}
        className="text-lg leading-snug font-semibold outline-none"
      >
        {endedText(ended.reason)}
      </h2>

      {waiting ? (
        <>
          <p role="status" className="text-muted-foreground">
            {polled.slow ? TALK_TEXT.diarySlow : TALK_TEXT.diaryWaiting}
          </p>
          {polled.slow && (
            <div className="flex flex-col gap-2 sm:flex-row">
              {ended.recordDate !== null && (
                <Button asChild className="sm:flex-1">
                  <Link to={`/diary/${ended.recordDate}`}>{TALK_TEXT.toDiary}</Link>
                </Button>
              )}
              <Button asChild variant="outline" className="sm:flex-1">
                <Link to="/">{TALK_TEXT.toHome}</Link>
              </Button>
            </div>
          )}
        </>
      ) : (
        <>
          <p className="text-muted-foreground">{TALK_TEXT.noDiary}</p>
          <div className="flex flex-col gap-2 sm:flex-row">
            <Button asChild className="sm:flex-1">
              <Link to="/">{TALK_TEXT.toHome}</Link>
            </Button>
            <Button asChild variant="outline" className="sm:flex-1">
              <Link to="/diary">{TALK_TEXT.toDiaryList}</Link>
            </Button>
            <Button type="button" variant="ghost" onClick={onTalkAgain} className="sm:flex-1">
              {TALK_TEXT.talkAgain}
            </Button>
          </div>
        </>
      )}
    </section>
  );
}
