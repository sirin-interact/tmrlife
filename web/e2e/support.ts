import { expect, test as base, type Locator, type Page } from '@playwright/test';

// 브라우저 흐름 테스트가 함께 쓰는 것들. 띄우는 일은 저장소 루트의 `make e2e`(web/e2e/run.sh)가 한다.

export const PASSWORD = 'dawn-over-the-quiet-harbor-27';
export const SESSION_COOKIE = 'naeil_session';

/** 돌릴 때마다 다른 주소를 쓴다. 같은 DB로 다시 돌려도, 시나리오끼리도 부딪히지 않는다. */
export function uniqueEmail(): string {
  const random = Math.random().toString(36).slice(2, 10);
  return `e2e-${Date.now()}-${random}@example.com`;
}

/**
 * 미리보기 서버는 운영 웹 서버와 같은 콘텐츠 보안 정책을 붙인다(vite.config.ts가 Caddyfile에서 읽어 온다).
 * 정책에 막힌 것이 하나라도 있으면 시나리오를 실패시킨다. <style>을 끼워 넣는 라이브러리를 들였을 때
 * 운영에서만 화면이 깨지는 일을 여기서 먼저 잡는다.
 */
export const test = base.extend<{ contentSecurityPolicy: void }>({
  contentSecurityPolicy: [
    async ({ page }, use) => {
      const violations: string[] = [];
      // 화면을 새로 고치거나 옮겨도 이어지도록 페이지 밖에 모은다. 막힌 지시문과 주소만 남기고 화면의 글은 담지 않는다.
      await page.exposeFunction('reportCspViolation', (violation: string) => {
        violations.push(violation);
      });
      await page.addInitScript(() => {
        document.addEventListener('securitypolicyviolation', (event) => {
          const report = (window as unknown as { reportCspViolation: (violation: string) => void })
            .reportCspViolation;
          report(`${event.effectiveDirective} ${event.blockedURI}`);
        });
      });

      await use();

      const policy = await page.evaluate(async () => {
        const response = await fetch('/', { method: 'HEAD' });
        return response.headers.get('Content-Security-Policy');
      });
      // 정책이 붙어 있지 않으면 아래 확인은 아무것도 보지 않은 것이다.
      expect(policy).toContain("style-src 'self'");
      expect(violations).toEqual([]);
    },
    { auto: true },
  ],
});

export { expect };

/**
 * 화면으로 가입한다. 가입이 끝나면 처음 화면에 서 있고 세션 쿠키가 브라우저에 있다.
 * 가입 화면 자체를 보는 것은 auth.spec.ts가 한다. 여기서는 로그인한 상태로 가는 가장 짧은 길로 쓴다.
 */
export async function signUp(page: Page, email: string, name?: string): Promise<void> {
  await page.goto('/signup');
  await page.getByLabel('이메일').fill(email);
  await page.getByLabel('비밀번호', { exact: true }).fill(PASSWORD);
  if (name !== undefined) await page.getByLabel(/부를 이름/).fill(name);

  const consents = page.getByRole('group', { name: /필수 동의/ });
  for (const label of [/서비스 이용약관/, /개인정보 처리방침/, /민감정보/, /외부 AI 서비스/]) {
    await consents.getByRole('checkbox', { name: label }).check();
  }
  await page.getByRole('button', { name: '가입하기' }).click();
  await expect(page).toHaveURL('/');
}

// ---- 대화 화면 ---------------------------------------------------------------------

/** 대화를 여는 첫 안부. 서버의 문구와 같아야 한다. */
export const OPENING = '오늘 하루는 어땠어요?';

/** 여러 뜻으로 읽히는 말에 되묻는 말의 끝. 모델이 만든 되물음과 미리 써 둔 문형이 모두 이 물음으로 끝난다. */
export const REFLECT_QUESTION = '오늘 무슨 일 있었어요?';

/** 되물은 뒤에도 뜻이 풀리지 않을 때 한 번 직접 묻는 말 */
export const DIRECT_ASK = '혹시 죽고 싶다는 생각도 들어요?';

/** 죽고 싶다는 생각이나 자해를 직접 말했을 때의 첫 응답. 미리 써 둔 문구 그대로 나간다. */
export const CRISIS_REPLY = '말해줘서 고마워요. 그런 마음을 혼자 안고 있었네요.';

export const talkLog = (page: Page): Locator => page.getByRole('list', { name: '대화 내용' });
export const aiMessages = (page: Page): Locator =>
  talkLog(page).getByRole('listitem').filter({ hasText: '내일:' });
export const userMessages = (page: Page): Locator =>
  talkLog(page).getByRole('listitem').filter({ hasText: '나:' });
export const composer = (page: Page): Locator => page.getByLabel('하고 싶은 이야기');

/** 대화 화면을 열고 첫 안부가 올 때까지 기다린다. */
export async function openTalk(page: Page): Promise<void> {
  await page.getByRole('link', { name: '오늘 이야기하기' }).click();
  await expect(page).toHaveURL('/talk');
  await expect(aiMessages(page).first()).toContainText(OPENING);
}

/** 글을 하나 보내고 답이 올 때까지 기다린다. 돌아오는 값은 그 답의 글이다. */
export async function say(page: Page, text: string): Promise<string> {
  const answered = await aiMessages(page).count();
  await composer(page).fill(text);
  // 보내기는 연결이 준비되고 앞선 답을 다 받은 뒤에만 열린다. 열리기 전에 누르면 글이 나가지 않는다.
  await expect(page.getByRole('button', { name: '보내기' })).toBeEnabled();
  await composer(page).press('Enter');
  // 답이 하나 늘 때까지 기다린다. 보내는 중 표시와 준비 중 표시는 그 사이에 지나간다.
  await expect(aiMessages(page)).toHaveCount(answered + 1);
  return (await aiMessages(page).nth(answered).innerText()).replace('내일:', '').trim();
}

/** 끝내기를 누르고 한 번 더 확인한다. */
export async function endTalk(page: Page): Promise<void> {
  await page.getByRole('button', { name: '끝내기' }).click();
  await page.getByRole('button', { name: '네, 끝낼게요' }).click();
}

/** 주소에서 기록 날짜를 읽는다. 기록 날짜는 서버가 정한다. 테스트가 시각에서 계산하지 않는다. */
export function recordDateFromUrl(url: string): string {
  const date = /\/diary\/(\d{4}-\d{2}-\d{2})/.exec(new URL(url).pathname)?.[1];
  if (date === undefined) throw new Error(`일기 주소가 아니다: ${new URL(url).pathname}`);
  return date;
}
