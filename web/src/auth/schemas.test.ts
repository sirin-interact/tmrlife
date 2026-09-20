import { describe, expect, it } from 'vitest';

import { createSignupSchema, loginSchema } from '@/auth/schemas';
import { testRequirements } from '@/test/mockApi';

const requirements = { ...testRequirements, consents: [...testRequirements.consents] };
const allKinds = requirements.consents.map((consent) => consent.kind);

const valid = {
  email: 'dawn@example.com',
  password: 'correct horse battery',
  displayName: '',
  agreed: allKinds,
};

function messages(result: ReturnType<ReturnType<typeof createSignupSchema>['safeParse']>) {
  if (result.success) return {};
  return Object.fromEntries(
    result.error.issues.map((issue) => [issue.path.join('.'), issue.message]),
  );
}

describe('로그인 폼 규칙', () => {
  it('이메일은 앞뒤 공백을 떼고, 비밀번호는 있는 그대로 둔다', () => {
    const result = loginSchema.parse({ email: '  dawn@example.com ', password: '  spaced  ' });

    expect(result).toEqual({ email: 'dawn@example.com', password: '  spaced  ' });
  });

  it('비밀번호 규칙을 보지 않는다. 짧은 옛 비밀번호로도 로그인할 수 있어야 한다', () => {
    expect(loginSchema.safeParse({ email: 'dawn@example.com', password: 'old' }).success).toBe(
      true,
    );
  });

  it('비어 있는 이메일에는 "입력해 주세요" 하나만 말한다', () => {
    const result = loginSchema.safeParse({ email: '   ', password: 'x' });

    expect(result.success).toBe(false);
    expect(result.error?.issues.map((issue) => issue.message)).toEqual(['이메일을 입력해 주세요.']);
  });
});

describe('가입 폼 규칙', () => {
  const schema = createSignupSchema(requirements);

  it('모두 맞으면 통과하고, 부를 이름은 앞뒤 공백을 뗀다', () => {
    const result = schema.parse({ ...valid, displayName: '  새벽 ' });

    expect(result.displayName).toBe('새벽');
  });

  it('부를 이름의 길이는 글자 수로 센다', () => {
    expect(schema.safeParse({ ...valid, displayName: '🌅'.repeat(40) }).success).toBe(true);
    expect(messages(schema.safeParse({ ...valid, displayName: '🌅'.repeat(41) }))).toEqual({
      displayName: '이름은 40자까지 쓸 수 있어요.',
    });
  });

  it('여러 칸이 틀렸으면 한 번에 모두 알려준다', () => {
    const result = schema.safeParse({
      email: 'nope',
      password: 'short',
      displayName: '',
      agreed: [],
    });

    expect(messages(result)).toEqual({
      email: '이메일 주소를 다시 확인해 주세요.',
      password: '비밀번호는 10자 이상이어야 해요.',
      agreed: '필수 동의 항목을 모두 확인해 주세요.',
    });
  });

  it('필요한 동의가 하나라도 빠지면 통과하지 못한다', () => {
    const result = schema.safeParse({ ...valid, agreed: allKinds.slice(1) });

    expect(messages(result)).toEqual({ agreed: '필수 동의 항목을 모두 확인해 주세요.' });
  });

  it('이메일 주소의 앞부분과 같은 비밀번호는 받지 않는다', () => {
    const result = schema.safeParse({
      ...valid,
      email: 'dawnlight2026@example.com',
      password: 'DawnLight2026',
    });

    expect(messages(result)).toEqual({ password: '이메일 주소와 같은 비밀번호는 쓸 수 없어요.' });
  });

  it('서버 규칙을 아직 모르면 그 규칙에 기대는 검사는 건너뛴다', () => {
    const withoutRules = createSignupSchema(undefined);

    expect(withoutRules.safeParse({ ...valid, password: 'short', agreed: [] }).success).toBe(true);
    expect(messages(withoutRules.safeParse({ ...valid, password: '' }))).toEqual({
      password: '비밀번호를 입력해 주세요.',
    });
  });
});
