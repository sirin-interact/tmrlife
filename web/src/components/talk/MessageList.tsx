import { useEffect, useRef } from 'react';

import { Button } from '@/components/ui/button';
import { TALK_TEXT } from '@/content/talkText';
import { cn } from '@/lib/utils';
import type { ChatMessage } from '@/talk/conversationState';

interface MessageListProps {
  messages: readonly ChatMessage[];
  /** 답을 기다리는 중이면 맨 아래에 준비 중 표시를 둔다. */
  thinking: boolean;
  /** 다시 보내기를 누를 수 있는 때인지 */
  canRetry: boolean;
  onRetry: (clientMessageId: string) => void;
}

// 답을 준비하고 있다는 말은 대화 화면의 상태 줄이 한 번만 알린다. 여기서는 모양만 보여 준다.
function ThinkingIndicator() {
  return (
    <li aria-hidden="true" className="flex justify-start">
      <div className="talk-thinking talk-message talk-message--ai">
        {[0, 1, 2].map((dot) => (
          <span key={dot} aria-hidden="true" className="talk-thinking__dot" />
        ))}
      </div>
    </li>
  );
}

/**
 * 대화 내용 전체. "대화 내용"을 열었을 때 보이고, 새 말이 오면 맨 아래로 내려간다.
 * 이 목록 자체는 화면 낭독기에 새 말을 알리지 않는다. 이어가는 대화에서는 지난 발화가 한꺼번에 들어오는데,
 * 그것까지 모두 읽어 주면 안 되기 때문이다. 방금 도착한 말만 대화 화면의 알림 영역이 따로 읽어 준다.
 */
export function MessageList({ messages, thinking, canRetry, onRetry }: MessageListProps) {
  const endRef = useRef<HTMLLIElement>(null);

  useEffect(() => {
    endRef.current?.scrollIntoView?.({ block: 'end' });
  }, [messages.length, thinking]);

  return (
    <ol aria-label={TALK_TEXT.logLabel} className="talk-messages">
      {messages.map((message) => {
        const mine = message.speaker === 'user';
        return (
          <li
            key={message.key}
            className={cn(
              'talk-message-entry flex flex-col gap-1',
              mine ? 'items-end' : 'items-start',
            )}
          >
            <div
              className={cn(
                'talk-message',
                mine ? 'talk-message--user' : 'talk-message--ai',
                message.delivery === 'sending' && 'opacity-70',
              )}
            >
              <span className="sr-only">
                {mine ? TALK_TEXT.speakerUser : TALK_TEXT.speakerAi}:{' '}
              </span>
              {message.text}
              {message.delivery === 'sending' && (
                <span className="sr-only"> ({TALK_TEXT.sending})</span>
              )}
            </div>
            {message.delivery === 'failed' && message.clientMessageId !== null && (
              <div className="flex items-center gap-1 text-sm text-destructive">
                <span>{TALK_TEXT.sendFailed}</span>
                <Button
                  type="button"
                  variant="link"
                  size="sm"
                  disabled={!canRetry}
                  onClick={() => onRetry(message.clientMessageId!)}
                  className="h-12 px-2"
                >
                  {TALK_TEXT.resend}
                </Button>
              </div>
            )}
          </li>
        );
      })}
      {thinking && <ThinkingIndicator />}
      <li ref={endRef} aria-hidden="true" />
    </ol>
  );
}
