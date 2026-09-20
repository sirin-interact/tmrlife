import { SendHorizontalIcon } from 'lucide-react';
import { useId, useRef, useState, type FormEvent, type KeyboardEvent } from 'react';

import { Button } from '@/components/ui/button';
import { TALK_TEXT } from '@/content/talkText';
import { textLength, USER_TEXT_MAX_LENGTH } from '@/talk/messages';

interface ComposerProps {
  /** 지금 글을 보낼 수 있는지. 연결 중이거나 답을 기다리는 동안에는 보낼 수 없지만 쓰는 것은 막지 않는다. */
  canSend: boolean;
  /** 보냈으면 true를 돌려준다. 그때만 입력란을 비운다. */
  onSend: (text: string) => boolean;
}

// 한글, 일본어처럼 글자를 조합해서 넣는 입력기는 조합을 끝내는 Enter에도 keydown을 낸다.
// 그 Enter로 보내 버리면 마지막 글자가 빠지거나 두 번 들어간다. 사파리는 그 순간 isComposing이 거짓이라 keyCode(229)도 함께 본다.
function isComposing(event: KeyboardEvent<HTMLTextAreaElement>): boolean {
  return event.nativeEvent.isComposing || event.keyCode === 229;
}

/** 글을 쓰는 자리. Enter로 보내고 Shift+Enter로 줄을 바꾼다. */
export function Composer({ canSend, onSend }: ComposerProps) {
  const [text, setText] = useState('');
  const inputId = useId();
  const hintId = useId();
  const errorId = useId();
  const inputRef = useRef<HTMLTextAreaElement>(null);

  const tooLong = textLength(text.trim()) > USER_TEXT_MAX_LENGTH;
  const sendable = canSend && text.trim() !== '' && !tooLong;

  function submit() {
    if (!sendable) return;
    if (onSend(text)) setText('');
    // 보내기 버튼을 눌러 보냈어도 다음 말을 바로 이어 쓸 수 있게 한다.
    inputRef.current?.focus();
  }

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    submit();
  }

  function handleKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key !== 'Enter' || event.shiftKey || isComposing(event)) return;
    // 보낼 수 없는 때에도 줄바꿈을 넣지 않는다. Enter가 어떤 때는 보내고 어떤 때는 줄을 바꾸면 헷갈린다.
    event.preventDefault();
    submit();
  }

  return (
    <form
      onSubmit={handleSubmit}
      className="flex flex-col gap-1 border-t bg-background pt-3 pb-[max(0.75rem,env(safe-area-inset-bottom))]"
    >
      {tooLong && (
        <p id={errorId} role="alert" className="text-sm text-destructive">
          {TALK_TEXT.tooLong}
        </p>
      )}
      <div className="flex items-end gap-2">
        <label htmlFor={inputId} className="sr-only">
          {TALK_TEXT.inputLabel}
        </label>
        <textarea
          id={inputId}
          ref={inputRef}
          rows={1}
          value={text}
          onChange={(event) => setText(event.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={TALK_TEXT.inputPlaceholder}
          enterKeyHint="send"
          aria-invalid={tooLong}
          aria-describedby={tooLong ? `${errorId} ${hintId}` : hintId}
          // 글자 크기를 16px 아래로 내리지 않는다. 더 작으면 iOS가 입력란에 초점이 갈 때 화면을 확대한다.
          className="field-sizing-content max-h-40 min-h-12 w-full min-w-0 flex-1 resize-none rounded-2xl border border-input bg-card px-4 py-3 text-base leading-normal text-foreground placeholder:text-muted-foreground aria-invalid:border-destructive"
        />
        <Button type="submit" size="icon" disabled={!sendable} aria-label={TALK_TEXT.send}>
          <SendHorizontalIcon aria-hidden="true" />
        </Button>
      </div>
      {/* 화면 키보드에는 Shift가 없다. 키보드가 달린 기기(태블릿, 데스크톱)의 너비에서만 보여 준다. */}
      <p id={hintId} className="hidden text-xs text-muted-foreground md:block">
        {TALK_TEXT.inputHint}
      </p>
    </form>
  );
}
