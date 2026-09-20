import { useEffect, useId, useRef, type ReactNode } from 'react';

import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

interface ConfirmPanelProps {
  title: string;
  children?: ReactNode;
  confirmLabel: string;
  cancelLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
  /** 되돌릴 수 없는 일이면 확인 버튼을 경고색으로 그린다. */
  destructive?: boolean;
  /** 요청을 보내는 중이면 두 버튼을 모두 꺼 둔다. */
  busy?: boolean;
  className?: string;
}

/**
 * 한 번 더 확인받는 자리. 화면 위에 겹쳐 띄우지 않고 누른 버튼이 있던 곳에 펼친다.
 * 겹쳐 띄우는 대화 상자 라이브러리는 <style>을 끼워 넣어서 운영 서버의 콘텐츠 보안 정책에 막힌다.
 * 나타나면 초점을 제목으로 옮겨 화면 낭독기가 무엇을 묻는지부터 읽게 하고, Esc로 그만둘 수 있다.
 */
export function ConfirmPanel({
  title,
  children,
  confirmLabel,
  cancelLabel,
  onConfirm,
  onCancel,
  destructive = false,
  busy = false,
  className,
}: ConfirmPanelProps) {
  const titleId = useId();
  const bodyId = useId();
  const titleRef = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    titleRef.current?.focus();
  }, []);

  return (
    // 키보드로 그만둘 수 있게 Esc를 듣는다. 누르는 대상은 안의 버튼들이다.
    // eslint-disable-next-line jsx-a11y/no-noninteractive-element-interactions
    <section
      role="alertdialog"
      aria-modal="false"
      aria-labelledby={titleId}
      aria-describedby={children === undefined ? undefined : bodyId}
      onKeyDown={(event) => {
        if (event.key === 'Escape' && !busy) onCancel();
      }}
      className={cn('flex flex-col gap-4 rounded-2xl border border-input bg-card p-5', className)}
    >
      <div className="flex flex-col gap-2">
        <h2
          id={titleId}
          ref={titleRef}
          tabIndex={-1}
          className="text-lg leading-snug font-semibold outline-none"
        >
          {title}
        </h2>
        {children !== undefined && (
          <div id={bodyId} className="flex flex-col gap-2 text-muted-foreground">
            {children}
          </div>
        )}
      </div>
      <div className="flex flex-col gap-2 sm:flex-row-reverse">
        <Button
          type="button"
          variant={destructive ? 'destructive' : 'default'}
          onClick={onConfirm}
          disabled={busy}
          className="sm:flex-1"
        >
          {confirmLabel}
        </Button>
        <Button
          type="button"
          variant="outline"
          onClick={onCancel}
          disabled={busy}
          className="sm:flex-1"
        >
          {cancelLabel}
        </Button>
      </div>
    </section>
  );
}
