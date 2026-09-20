import { CheckIcon, CircleIcon } from 'lucide-react';

import type { AuthRequirements } from '@/api/types';
import { checkPassword } from '@/auth/passwordPolicy';
import { cn } from '@/lib/utils';

interface PasswordHintsProps {
  id: string;
  password: string;
  email: string;
  rules: AuthRequirements['password'];
}

/**
 * 입력하는 동안 비밀번호 규칙을 얼마나 채웠는지 보여 준다. 규칙의 숫자는 서버가 알려준 값이다.
 * 글자를 칠 때마다 읽어 주면 시끄러우므로 실시간 알림 영역으로 만들지 않는다.
 * 입력란의 설명(aria-describedby)으로 이어져 있어서 초점이 갈 때 지금 상태가 읽힌다.
 */
export function PasswordHints({ id, password, email, rules }: PasswordHintsProps) {
  const check = checkPassword(password, email, rules);
  const started = password !== '';

  const hints = [
    { text: `${rules.min_length}자 이상`, met: started && !check.tooShort },
    { text: '이메일 주소와 다르게', met: started && !check.matchesEmail },
  ];

  return (
    <ul id={id} className="flex flex-col gap-1 text-sm text-muted-foreground">
      {hints.map(({ text, met }) => (
        <li key={text} className={cn('flex items-center gap-2', met && 'text-foreground')}>
          {met ? (
            <CheckIcon className="size-4 shrink-0" aria-hidden="true" />
          ) : (
            <CircleIcon className="size-4 shrink-0 opacity-60" aria-hidden="true" />
          )}
          <span>
            {text}
            {/* 채웠는지를 모양으로만 알리지 않는다. */}
            <span className="sr-only">{met ? ', 채웠어요.' : ', 아직이에요.'}</span>
          </span>
        </li>
      ))}
      {/* 상한은 넘었을 때만 말한다. 바이트로 세는 값이라 미리 숫자로 안내하면 오히려 헷갈린다. */}
      {check.tooLong && <li className="text-destructive">너무 길어요. 조금 줄여 주세요.</li>}
    </ul>
  );
}
