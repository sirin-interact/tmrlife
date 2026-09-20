import { EyeIcon, EyeOffIcon } from 'lucide-react';
import { useState, type ComponentProps } from 'react';

import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';

type PasswordInputProps = Omit<ComponentProps<'input'>, 'type'>;

/** 비밀번호 입력란. 폰의 작은 자판에서는 오타가 잦아서, 입력한 글자를 눈으로 확인할 수 있게 한다. */
export function PasswordInput({ className, disabled, ...props }: PasswordInputProps) {
  const [visible, setVisible] = useState(false);

  return (
    <div className="relative">
      <Input
        type={visible ? 'text' : 'password'}
        disabled={disabled}
        // 보이는 상태에서는 자판이 낱말을 고치거나 첫 글자를 대문자로 바꾸지 않게 한다.
        autoCapitalize="none"
        autoCorrect="off"
        spellCheck={false}
        className={cn('pr-14', className)}
        {...props}
      />
      <button
        type="button"
        // 이름은 그대로 두고 눌림 상태만 바꾼다. 이름까지 바꾸면 화면 낭독기가 상태를 두 번 말한다.
        aria-label="비밀번호 보기"
        aria-pressed={visible}
        disabled={disabled}
        onClick={() => setVisible((current) => !current)}
        className="absolute inset-y-0 right-0 flex w-12 items-center justify-center rounded-lg text-muted-foreground transition-colors hover:text-foreground disabled:pointer-events-none disabled:opacity-50"
      >
        {visible ? (
          <EyeOffIcon className="size-5" aria-hidden="true" />
        ) : (
          <EyeIcon className="size-5" aria-hidden="true" />
        )}
      </button>
    </div>
  );
}
