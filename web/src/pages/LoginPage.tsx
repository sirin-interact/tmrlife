import { ArrowRightIcon, LeafIcon } from 'lucide-react';
import { BrandScene } from '@/components/BrandScene';
import '@/styles/auth.css';
import { zodResolver } from '@hookform/resolvers/zod';
import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { Link, useLocation } from 'react-router';

import { isApiError } from '@/api/errors';
import { loginSchema, type LoginFormValues } from '@/auth/schemas';
import { useLogin } from '@/auth/session';
import { describedBy } from '@/components/form/describedBy';
import { FieldError } from '@/components/form/FieldError';
import { FormErrorSummary } from '@/components/form/FormErrorSummary';
import { PasswordInput } from '@/components/PasswordInput';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardFooter, CardHeader } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { describeError, FORM_INVALID_MESSAGE } from '@/content/errorMessages';

export function LoginPage() {
  const location = useLocation();
  const login = useLogin();
  const [summary, setSummary] = useState<string | null>(null);

  const {
    register,
    handleSubmit,
    setFocus,
    formState: { errors, submitCount },
  } = useForm<LoginFormValues>({
    resolver: zodResolver(loginSchema),
    defaultValues: { email: '', password: '' },
  });

  // 성공한 뒤의 이동은 여기서 하지 않는다. 로그인 상태가 채워지면 경로 보호가 가려던 화면으로 보낸다.
  function onValid(values: LoginFormValues) {
    if (login.isPending) return;
    setSummary(null);
    login.mutate(values, {
      onError: (error) => {
        setSummary(describeError(error).message);
        // 어느 쪽이 틀렸는지 서버가 알려주지 않는다. 다시 치는 일이 많은 비밀번호 칸으로 보낸다.
        if (isApiError(error) && error.code === 'invalid_credentials') setFocus('password');
      },
    });
  }

  const fieldMessages = [errors.email?.message, errors.password?.message].filter(
    (message) => message !== undefined,
  );

  return (
    <div className="auth-layout">
      <title>로그인 · 내일</title>
      <BrandScene />
      <div className="auth-form-panel">
        <div className="auth-heading">
          <span className="auth-heading__eyebrow">다시 만나요</span>
          <h1 className="text-2xl leading-snug font-semibold">다시 만나서 반가워요</h1>
          <p className="text-muted-foreground">오늘 하루도 편하게 이야기해 주세요.</p>
        </div>

        <Card className="auth-card">
          <form
            onSubmit={(event) =>
              void handleSubmit(onValid, () => setSummary(FORM_INVALID_MESSAGE))(event)
            }
            noValidate
            aria-busy={login.isPending}
            className="flex flex-col gap-6"
          >
            <CardHeader>
              <h2 className="text-xl leading-snug font-semibold">로그인</h2>
            </CardHeader>

            <CardContent className="flex flex-col gap-5">
              <div className="flex flex-col gap-2">
                <Label htmlFor="login-email">이메일</Label>
                <Input
                  id="login-email"
                  type="email"
                  inputMode="email"
                  // 비밀번호 관리 앱은 이 값을 보고 계정 칸을 알아본다.
                  autoComplete="username"
                  autoCapitalize="none"
                  spellCheck={false}
                  required
                  aria-invalid={errors.email !== undefined}
                  aria-describedby={describedBy(errors.email && 'login-email-error')}
                  {...register('email')}
                />
                <FieldError id="login-email-error" message={errors.email?.message} />
              </div>

              <div className="flex flex-col gap-2">
                <Label htmlFor="login-password">비밀번호</Label>
                <PasswordInput
                  id="login-password"
                  autoComplete="current-password"
                  required
                  aria-invalid={errors.password !== undefined}
                  aria-describedby={describedBy(errors.password && 'login-password-error')}
                  {...register('password')}
                />
                <FieldError id="login-password-error" message={errors.password?.message} />
              </div>
            </CardContent>

            <CardFooter>
              <FormErrorSummary key={submitCount} message={summary} details={fieldMessages} />
              <Button
                type="submit"
                size="lg"
                className="auth-submit w-full"
                disabled={login.isPending}
              >
                {login.isPending ? '로그인하고 있어요' : '로그인'}
                <ArrowRightIcon aria-hidden="true" className="size-4" />
              </Button>
            </CardFooter>
          </form>
        </Card>

        {/* 가려던 주소를 가입 화면으로도 넘긴다. 가입을 마치면 거기로 간다. */}
        <p className="auth-switch">
          처음 오셨나요?{' '}
          <Link
            to="/signup"
            state={location.state as unknown}
            className="inline-flex min-h-touch items-center font-semibold text-link underline underline-offset-4"
          >
            가입하기
          </Link>
        </p>
        <p className="auth-privacy">
          <LeafIcon aria-hidden="true" className="size-3" />
          나의 속도로, 편안하게 시작해요
        </p>
      </div>
    </div>
  );
}
