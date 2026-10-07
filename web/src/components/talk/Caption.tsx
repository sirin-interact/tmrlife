import { Button } from '@/components/ui/button';
import { TALK_TEXT } from '@/content/talkText';
import { cn } from '@/lib/utils';
import type { ChatMessage } from '@/talk/conversationState';
import { latestExchange } from '@/talk/latestExchange';

interface CaptionProps {
  messages: readonly ChatMessage[];
  /** 지금 말하고 있는 한 마디의 중간 자막. 있으면 사용자의 말 자리에 이것이 보인다. */
  partial: string | null;
  /** 답을 기다리는 중이면 사용자의 말 아래에 준비 중 점을 둔다. 말로 알리는 것은 화면의 상태 줄이 한다. */
  thinking: boolean;
  /** 다시 보내기를 누를 수 있는 때인지 */
  canRetry: boolean;
  onRetry: (clientMessageId: string) => void;
}

function ThinkingDots() {
  return (
    <div aria-hidden="true" className="talk-thinking">
      {[0, 1, 2].map((dot) => (
        <span key={dot} className="talk-thinking__dot" />
      ))}
    </div>
  );
}

/**
 * 대화 화면 가운데의 자막. 지난 말을 쌓아 두지 않고 지금의 한 마디만 보여 준다.
 * 음성 대화에서 말한 내용과 들은 답이 글로 함께 보이는 자리이고, 글로 이야기할 때도 같은 자리를 쓴다.
 * 말하는 중에는 알아듣고 있는 말이 사용자의 자리에 실시간으로 보인다.
 * 지난 말까지 보려면 "대화 내용"을 연다.
 */
export function Caption({ messages, partial, thinking, canRetry, onRetry }: CaptionProps) {
  const { user, ai } = latestExchange(messages);

  return (
    <section aria-label={TALK_TEXT.captionLabel} className="talk-caption">
      {partial !== null ? (
        // 아직 확정되지 않은 말이라 옅게 두고, 말이 이어지고 있다는 뜻으로 줄임표를 붙인다.
        <p className="talk-caption__user talk-caption__partial">
          <span className="sr-only">
            {TALK_TEXT.speakerUser} ({TALK_TEXT.partialLabel}):{' '}
          </span>
          {partial}
          <span aria-hidden="true">…</span>
        </p>
      ) : (
        user !== null && (
          <div key={user.key} className="talk-caption__exchange flex flex-col items-center gap-1">
            <p className={cn('talk-caption__user', user.delivery === 'sending' && 'opacity-70')}>
              <span className="sr-only">{TALK_TEXT.speakerUser}: </span>
              {user.text}
              {user.delivery === 'sending' && (
                <span className="sr-only"> ({TALK_TEXT.sending})</span>
              )}
            </p>
            {user.delivery === 'failed' && user.clientMessageId !== null && (
              <div className="flex items-center gap-1 text-sm text-destructive">
                <span>{TALK_TEXT.sendFailed}</span>
                <Button
                  type="button"
                  variant="link"
                  size="sm"
                  disabled={!canRetry}
                  onClick={() => onRetry(user.clientMessageId!)}
                  className="h-12 px-2"
                >
                  {TALK_TEXT.resend}
                </Button>
              </div>
            )}
          </div>
        )
      )}
      {ai !== null && (
        <p key={ai.key} className="talk-caption__answer">
          <span className="sr-only">{TALK_TEXT.speakerAi}: </span>
          {ai.text}
        </p>
      )}
      {thinking && <ThinkingDots />}
    </section>
  );
}
