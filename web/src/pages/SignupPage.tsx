import '@/styles/auth.css';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMemo, useState } from 'react';
import { Controller, useForm, useWatch } from 'react-hook-form';
import { Link, useLocation } from 'react-router';

import { isApiError } from '@/api/errors';
import { createSignupSchema, type SignupFormValues } from '@/auth/schemas';
import { useAuthRequirements, useSignup } from '@/auth/session';
import { ConsentFields } from '@/components/ConsentFields';
import { describedBy } from '@/components/form/describedBy';
import { FieldError } from '@/components/form/FieldError';
import { FormErrorSummary } from '@/components/form/FormErrorSummary';
import { MedicalNotice } from '@/components/MedicalNotice';
import { PasswordHints } from '@/components/PasswordHints';
import { PasswordInput } from '@/components/PasswordInput';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardFooter, CardHeader } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { CONSENT_TEXT, consentCopyFor } from '@/content/consentCopy';
import { describeError, FORM_INVALID_MESSAGE, type FormFieldName } from '@/content/errorMessages';
import { browserTimeZone } from '@/lib/timezone';

// 서버 오류가 가리키는 자리와 폼의 필드 이름을 잇는다.
const FORM_FIELD: Record<FormFieldName, keyof SignupFormValues> = {
  email: 'email',
  password: 'password',
  displayName: 'displayName',
  consents: 'agreed',
};

export function SignupPage() {
  const location = useLocation();
  const requirements = useAuthRequirements();
  const signup = useSignup();
  const [summary, setSummary] = useState<string | null>(null);

  const rules = requirements.data;
  const schema = useMemo(() => createSignupSchema(rules), [rules]);

  const {
    register,
    control,
    handleSubmit,
    setError,
    getValues,
    setValue,
    formState: { errors, submitCount },
  } = useForm<SignupFormValues>({
    resolver: zodResolver(schema),
    defaultValues: { email: '', password: '', displayName: '', agreed: [] },
  });

  const password = useWatch({ control, name: 'password' });
  const email = useWatch({ control, name: 'email' });

  // 이 앱이 설명을 갖고 있지 않은 동의를 서버가 요구하면 가입을 받지 않는다. 종류를 모를 때도, 판이 다를 때도 그렇다.
  // 무엇에 동의하는지 보여 주지 못한 채로 동의를 받을 수는 없다.
  const hasUnknownConsent =
    rules?.consents.some(({ kind, version }) => consentCopyFor(kind, version) === undefined) ??
    false;
  const canSubmit = rules !== undefined && !hasUnknownConsent && !signup.isPending;

  function onValid(values: SignupFormValues) {
    if (!rules || !canSubmit) return;
    setSummary(null);

    const timezone = browserTimeZone();
    signup.mutate(
      {
        email: values.email,
        // 비밀번호는 다듬지 않고 그대로 보낸다. 앞뒤 공백도 비밀번호의 일부다.
        password: values.password,
        ...(values.displayName !== '' && { display_name: values.displayName }),
        ...(timezone !== undefined && { timezone }),
        // 서버가 준 동의 항목을 판(version)까지 그대로 돌려보낸다. 사용자가 본 문서의 판이 기록으로 남는다.
        // 화면에 보인 글이 그 판의 글이라는 것은 위의 hasUnknownConsent가 지킨다.
        consents: rules.consents.filter(({ kind }) => values.agreed.includes(kind)),
      },
      { onError: handleServerError },
    );
  }

  // 성공한 뒤의 이동은 여기서 하지 않는다. 로그인 상태가 채워지면 경로 보호가 처음 화면으로 보낸다.
  function handleServerError(error: unknown) {
    const description = describeError(error, {
      passwordMinLength: rules?.password.min_length,
      displayNameMaxLength: rules?.display_name_max_length,
    });
    setSummary(description.message);

    let focused = false;
    for (const [name, message] of Object.entries(description.fields)) {
      // 첫 번째로 틀린 칸에만 초점을 보낸다.
      setError(
        FORM_FIELD[name as FormFieldName],
        { type: 'server', message },
        { shouldFocus: !focused },
      );
      focused = true;
    }

    // 동의 문서가 새 판으로 바뀌었다. 규칙을 다시 받아 오고, 바뀐 항목은 새 내용을 보고 다시 고르게 한다.
    const outdated = isApiError(error) ? (error.consents?.outdated ?? []) : [];
    if (outdated.length > 0) {
      const stillAgreed = getValues('agreed').filter((kind) => !outdated.some((k) => k === kind));
      setValue('agreed', stillAgreed);
      void requirements.refetch();
    }
  }

  const fieldMessages = [
    errors.email?.message,
    errors.password?.message,
    errors.displayName?.message,
    errors.agreed?.message,
  ].filter((message) => message !== undefined);

  return (
    <div className="auth-layout">
      <title>가입하기 · 내일</title>
      <div className="auth-form-panel">
        <div className="auth-heading">
          <h1 className="text-2xl leading-snug font-semibold">내일을 시작해요</h1>
          <p className="text-muted-foreground">오늘을 말하면, 내일이 보여요.</p>
        </div>

        <Card>
          <form
            onSubmit={(event) =>
              void handleSubmit(onValid, () => setSummary(FORM_INVALID_MESSAGE))(event)
            }
            noValidate
            aria-busy={signup.isPending}
            className="flex flex-col gap-6"
          >
            <CardHeader>
              <h2 className="text-xl leading-snug font-semibold">가입하기</h2>
            </CardHeader>

            <CardContent className="flex flex-col gap-5">
              <div className="flex flex-col gap-2">
                <Label htmlFor="signup-email">이메일</Label>
                <Input
                  id="signup-email"
                  type="email"
                  inputMode="email"
                  autoComplete="email"
                  autoCapitalize="none"
                  spellCheck={false}
                  required
                  aria-invalid={errors.email !== undefined}
                  aria-describedby={describedBy(errors.email && 'signup-email-error')}
                  {...register('email')}
                />
                <FieldError id="signup-email-error" message={errors.email?.message} />
              </div>

              <div className="flex flex-col gap-2">
                <Label htmlFor="signup-password">비밀번호</Label>
                <PasswordInput
                  id="signup-password"
                  autoComplete="new-password"
                  required
                  aria-invalid={errors.password !== undefined}
                  aria-describedby={describedBy(
                    rules && 'signup-password-hints',
                    errors.password && 'signup-password-error',
                  )}
                  {...register('password')}
                />
                {rules && (
                  <PasswordHints
                    id="signup-password-hints"
                    password={password}
                    email={email}
                    rules={rules.password}
                  />
                )}
                <FieldError id="signup-password-error" message={errors.password?.message} />
              </div>

              <div className="flex flex-col gap-2">
                <Label htmlFor="signup-display-name">
                  부를 이름 <span className="font-normal text-muted-foreground">(선택)</span>
                </Label>
                <Input
                  id="signup-display-name"
                  autoComplete="nickname"
                  aria-invalid={errors.displayName !== undefined}
                  aria-describedby={describedBy(
                    'signup-display-name-hint',
                    errors.displayName && 'signup-display-name-error',
                  )}
                  {...register('displayName')}
                />
                <p id="signup-display-name-hint" className="text-sm text-muted-foreground">
                  내일이 인사할 때 부를 이름이에요. 비워 두어도 괜찮아요.
                </p>
                <FieldError id="signup-display-name-error" message={errors.displayName?.message} />
              </div>

              {requirements.isPending && (
                <p role="status" className="text-muted-foreground">
                  {CONSENT_TEXT.loading}
                </p>
              )}

              {requirements.isError && !rules && (
                <Alert variant="destructive">
                  <AlertDescription>
                    <p>{describeError(requirements.error).message}</p>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={requirements.isFetching}
                      onClick={() => void requirements.refetch()}
                    >
                      다시 불러오기
                    </Button>
                  </AlertDescription>
                </Alert>
              )}

              {rules && (
                <Controller
                  control={control}
                  name="agreed"
                  render={({ field, fieldState }) => (
                    <ConsentFields
                      consents={rules.consents}
                      value={field.value}
                      onChange={field.onChange}
                      error={fieldState.error?.message}
                      disabled={signup.isPending}
                      focusRef={field.ref}
                    />
                  )}
                />
              )}

              {hasUnknownConsent && (
                <Alert variant="destructive">
                  <AlertDescription>{CONSENT_TEXT.unknownKind}</AlertDescription>
                </Alert>
              )}
            </CardContent>

            <CardFooter>
              <FormErrorSummary key={submitCount} message={summary} details={fieldMessages} />
              <Button type="submit" size="lg" className="w-full" disabled={!canSubmit}>
                {signup.isPending ? '가입하고 있어요' : '가입하기'}
              </Button>
              <MedicalNotice className="text-center" />
            </CardFooter>
          </form>
        </Card>

        <p className="auth-switch">
          이미 가입하셨나요?{' '}
          <Link
            to="/login"
            state={location.state as unknown}
            className="inline-flex min-h-touch items-center font-semibold text-link underline underline-offset-4"
          >
            로그인
          </Link>
        </p>
      </div>
    </div>
  );
}
