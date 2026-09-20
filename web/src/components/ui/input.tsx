import * as React from 'react';

import { cn } from '@/lib/utils';

function Input({ className, type, ...props }: React.ComponentProps<'input'>) {
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(
        // 글자 크기를 16px 아래로 내리지 않는다. 더 작으면 iOS가 입력란에 초점이 갈 때 화면을 확대한다.
        'h-12 w-full min-w-0 rounded-lg border border-input bg-card px-4 text-base text-foreground transition-colors',
        'placeholder:text-muted-foreground',
        'disabled:cursor-not-allowed disabled:opacity-50',
        'aria-invalid:border-destructive',
        className,
      )}
      {...props}
    />
  );
}

export { Input };
