import * as z from 'zod/mini';

import type { AuthRequirements } from '@/api/types';
import { checkPassword } from '@/auth/passwordPolicy';
import { displayNameTooLongMessage, passwordReasonMessage } from '@/content/errorMessages';

// 검증 라이브러리는 쓰는 기능만 묶음에 들어가는 가벼운 판(zod/mini)을 쓴다.
// 폰에서 처음 여는 화면이 로그인과 가입이라 여기서 받는 코드의 크기가 곧 첫 화면의 속도다.

// 명세가 받는 최대 길이다. 넘으면 서버는 어느 칸이 틀렸는지 알려주지 않고 요청 전체를 거절한다.
// 그래서 어느 칸인지 말해 줄 수 있는 화면에서 먼저 걸러 낸다.
const EMAIL_MAX_LENGTH = 320;
const PASSWORD_MAX_LENGTH = 1024;

// 일부러 느슨하게 본다. 오타를 일찍 알려주려는 것일 뿐이고, 받을 수 있는 주소인지는 서버가 정한다.
// 서버보다 엄격하면 멀쩡한 주소를 가진 사람이 화면에서 막힌다.
const EMAIL_SHAPE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

const characterCount = (value: string) => Array.from(value).length;

// 비어 있을 때는 "입력해 주세요" 하나만 말한다. 꼴이 틀렸다는 말까지 겹쳐 나오면 무엇을 하라는 것인지 흐려진다.
const email = z.string().check(
  z.trim(),
  z.minLength(1, '이메일을 입력해 주세요.'),
  z.maxLength(EMAIL_MAX_LENGTH, '이메일 주소가 너무 길어요.'),
  z.refine((value) => value === '' || EMAIL_SHAPE.test(value), '이메일 주소를 다시 확인해 주세요.'),
);

export const loginSchema = z.object({
  email,
  // 로그인에서는 비밀번호 규칙을 보지 않는다. 규칙이 바뀌기 전에 만든 비밀번호로도 로그인할 수 있어야 한다.
  password: z.string().check(
    z.minLength(1, '비밀번호를 입력해 주세요.'),
    z.refine((value) => characterCount(value) <= PASSWORD_MAX_LENGTH, '비밀번호가 너무 길어요.'),
  ),
});

export type LoginFormValues = z.infer<typeof loginSchema>;

export const CONSENTS_REQUIRED_MESSAGE = '필수 동의 항목을 모두 확인해 주세요.';

/**
 * 가입 폼의 규칙. 길이 제한과 필요한 동의는 서버가 알려준 값을 쓴다.
 * 규칙을 아직 받지 못했을 때는 서버 규칙에 기대는 검사를 건너뛴다. 그동안은 제출 버튼이 꺼져 있다.
 */
export function createSignupSchema(requirements: AuthRequirements | undefined) {
  const requiredKinds = requirements?.consents.map((consent) => consent.kind) ?? [];
  const displayNameMax = requirements?.display_name_max_length;
  const passwordRules = requirements?.password;

  return z
    .object({
      email,
      password: z.string().check(
        z.minLength(1, '비밀번호를 입력해 주세요.'),
        z.superRefine((value, context) => {
          if (!passwordRules || value === '') return;
          const check = checkPassword(value, '', passwordRules);
          if (check.tooShort) {
            context.addIssue({
              code: 'custom',
              message: passwordReasonMessage('too_short', passwordRules.min_length),
            });
          }
          if (check.tooLong) {
            context.addIssue({ code: 'custom', message: passwordReasonMessage('too_long') });
          }
        }),
      ),
      displayName: z.string().check(
        z.trim(),
        z.refine(
          (value) => displayNameMax === undefined || characterCount(value) <= displayNameMax,
          displayNameTooLongMessage(displayNameMax),
        ),
      ),
      agreed: z
        .array(z.string())
        .check(
          z.refine(
            (agreed) => requiredKinds.every((kind) => agreed.includes(kind)),
            CONSENTS_REQUIRED_MESSAGE,
          ),
        ),
    })
    .check(
      // 이메일과 견주는 규칙만 폼 전체를 봐야 한다. 나머지는 칸마다 따로 검사해서 다른 칸이 틀렸어도 함께 알려준다.
      z.superRefine((values, context) => {
        if (!passwordRules) return;
        if (checkPassword(values.password, values.email, passwordRules).matchesEmail) {
          context.addIssue({
            code: 'custom',
            path: ['password'],
            message: passwordReasonMessage('matches_email'),
          });
        }
      }),
    );
}

export type SignupFormValues = z.infer<ReturnType<typeof createSignupSchema>>;
