import { expect, test, type APIRequestContext, type Page } from '@playwright/test';

// 실제 서버, 실제 DB, 빌드된 웹앱을 브라우저로 밟아 본다. 띄우는 일은 `make e2e`(web/e2e/run.sh)가 한다.

const PASSWORD = 'dawn-over-the-quiet-harbor-27';
const SESSION_COOKIE = 'naeil_session';

// 미리보기 서버는 운영 웹 서버와 같은 콘텐츠 보안 정책을 붙인다(vite.config.ts가 Caddyfile에서 읽어 온다).
// 정책에 막힌 것이 하나라도 있으면 시나리오를 실패시킨다. <style>을 끼워 넣는 라이브러리를 들였을 때
// 운영에서만 화면이 깨지는 일을 여기서 먼저 잡는다.
let cspViolations: string[] = [];

test.beforeEach(async ({ page }) => {
  cspViolations = [];
  // 화면을 새로 고치거나 옮겨도 이어지도록 페이지 밖에 모은다. 막힌 지시문과 주소만 남기고 화면의 글은 담지 않는다.
  await page.exposeFunction('reportCspViolation', (violation: string) => {
    cspViolations.push(violation);
  });
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (event) => {
      const report = (window as unknown as { reportCspViolation: (violation: string) => void })
        .reportCspViolation;
      report(`${event.effectiveDirective} ${event.blockedURI}`);
    });
  });
});

test.afterEach(async ({ page }) => {
  const policy = await page.evaluate(async () => {
    const response = await fetch('/', { method: 'HEAD' });
    return response.headers.get('Content-Security-Policy');
  });
  // 정책이 붙어 있지 않으면 아래 확인은 아무것도 보지 않은 것이다.
  expect(policy).toContain("style-src 'self'");
  expect(cspViolations).toEqual([]);
});

/** 돌릴 때마다 다른 주소를 쓴다. 같은 DB로 다시 돌려도, 시나리오끼리도 부딪히지 않는다. */
function uniqueEmail(): string {
  const random = Math.random().toString(36).slice(2, 10);
  return `e2e-${Date.now()}-${random}@example.com`;
}

const passwordField = (page: Page) => page.getByLabel('비밀번호', { exact: true });
const homeAction = (page: Page) => page.getByRole('link', { name: '오늘 이야기하기' });

async function sessionCookie(page: Page) {
  const cookies = await page.context().cookies();
  return cookies.find((cookie) => cookie.name === SESSION_COOKIE);
}

/** 화면을 거치지 않고 계정을 만든다. 브라우저와 쿠키를 나누지 않는 별도의 클라이언트로 부른다. */
async function createAccount(request: APIRequestContext, email: string): Promise<void> {
  const requirements = await request.get('/api/v1/auth/requirements');
  expect(requirements.ok()).toBe(true);
  const { consents } = (await requirements.json()) as { consents: unknown[] };

  const signup = await request.post('/api/v1/auth/signup', {
    data: { email, password: PASSWORD, consents },
  });
  expect(signup.status()).toBe(201);
}

test('가입하고, 새로 고쳐도 로그인이 이어지고, 로그아웃한 뒤에는 로그인해야 들어갈 수 있다', async ({
  page,
}) => {
  const email = uniqueEmail();

  await test.step('가입한다', async () => {
    await page.goto('/signup');
    await page.getByLabel('이메일').fill(email);
    await passwordField(page).fill(PASSWORD);
    await page.getByLabel(/부를 이름/).fill('새벽');

    // 의료 서비스가 아니라는 고지는 가입 화면에 늘 보인다.
    await expect(page.getByRole('note')).toHaveText(
      '내일은 의료 서비스가 아니며, 제공되는 정보는 진단이나 치료를 대신하지 않습니다.',
    );

    // 동의는 항목마다 따로 받는다. 하나라도 빠지면 가입되지 않는다.
    const consents = page.getByRole('group', { name: /필수 동의/ });

    // 글자가 아니라 줄의 빈 자리를 눌러도 눌린다. 폰에서 한 손으로 누르다 보면 닿는 자리다.
    // (줄의 왼쪽 끝은 넓혀 둔 체크박스의 영역이라 라벨의 오른쪽 위 구석을 누른다.)
    const termsRow = page.locator('label[for="consent-terms"]');
    const rowBox = await termsRow.boundingBox();
    if (!rowBox) throw new Error('약관 동의 줄이 화면에 없다');
    expect(rowBox.height).toBeGreaterThanOrEqual(48);
    const corner = { x: rowBox.width - 4, y: 4 };
    await termsRow.click({ position: corner });
    await expect(consents.getByRole('checkbox', { name: /서비스 이용약관/ })).toBeChecked();
    await termsRow.click({ position: corner });
    await expect(consents.getByRole('checkbox', { name: /서비스 이용약관/ })).not.toBeChecked();

    // 전문을 읽고 돌아와도 쓰던 폼이 그대로다.
    await page.getByRole('link', { name: '개인정보 처리방침 읽기' }).click();
    const sheet = page.getByRole('dialog', { name: '개인정보 처리방침' });
    await expect(sheet.getByRole('heading', { name: '어떤 정보를 받나요' })).toBeVisible();
    await sheet.getByRole('button', { name: '닫고 돌아가기' }).first().click();
    await expect(sheet).toHaveCount(0);
    await expect(page.getByLabel('이메일')).toHaveValue(email);

    await consents.getByRole('checkbox', { name: /서비스 이용약관/ }).check();
    await consents.getByRole('checkbox', { name: /개인정보 처리방침/ }).check();
    await consents.getByRole('checkbox', { name: /민감정보/ }).check();
    await page.getByRole('button', { name: '가입하기' }).click();
    await expect(page.getByRole('alert')).toContainText('필수 동의 항목을 모두 확인해 주세요.');
    await expect(page).toHaveURL('/signup');

    await consents.getByRole('checkbox', { name: /외부 AI 서비스/ }).check();
    await page.getByRole('button', { name: '가입하기' }).click();
  });

  await test.step('처음 화면에 도착한다', async () => {
    await expect(page).toHaveURL('/');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('새벽님, 안녕하세요');
    await expect(homeAction(page)).toHaveAttribute('href', '/talk');

    // 세션 쿠키는 스크립트가 읽을 수 없고, 다른 사이트에서 온 요청에는 실리지 않는다.
    const cookie = await sessionCookie(page);
    expect(cookie).toMatchObject({ httpOnly: true, sameSite: 'Lax', path: '/' });
    expect(await page.evaluate(() => document.cookie)).not.toContain(SESSION_COOKIE);
  });

  await test.step('새로 고쳐도 로그인이 이어진다', async () => {
    await page.reload();
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('새벽님, 안녕하세요');
    await expect(page).toHaveURL('/');
  });

  await test.step('로그인한 채로 로그인 화면에 가면 처음 화면으로 돌아온다', async () => {
    await page.goto('/login');
    await expect(page).toHaveURL('/');
    await expect(homeAction(page)).toBeVisible();
  });

  await test.step('로그아웃한다', async () => {
    await page.getByRole('button', { name: '로그아웃' }).click();
    await expect(page).toHaveURL('/login');
    await expect(page.getByRole('heading', { level: 2, name: '로그인' })).toBeVisible();
    expect(await sessionCookie(page)).toBeUndefined();
  });

  await test.step('로그인이 필요한 화면은 로그인 화면으로 보낸다', async () => {
    await page.goto('/');
    await expect(page).toHaveURL('/login');
    await expect(homeAction(page)).toHaveCount(0);
  });

  await test.step('로그인하면 가려던 화면으로 돌아간다', async () => {
    await page.getByLabel('이메일').fill(email);
    await passwordField(page).fill(PASSWORD);
    await page.getByRole('button', { name: '로그인' }).click();

    await expect(page).toHaveURL('/');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('새벽님, 안녕하세요');
  });

  await test.step('서비스 워커는 /api와 /ws의 응답을 어디에도 담아 두지 않는다', async () => {
    const cachedPaths = await page.evaluate(async () => {
      await navigator.serviceWorker.ready;
      const paths: string[] = [];
      for (const name of await caches.keys()) {
        const cache = await caches.open(name);
        for (const request of await cache.keys()) paths.push(new URL(request.url).pathname);
      }
      return paths;
    });

    // 앱 껍데기는 담겨 있어야 한다. 비어 있다면 아래 확인은 아무것도 보지 않은 것이다.
    expect(cachedPaths).toContain('/index.html');
    expect(cachedPaths.filter((path) => /^\/(?:api|ws)(?:\/|$)/.test(path))).toEqual([]);
  });
});

test('비밀번호가 틀리면 한국어로 알려주고, 맞게 넣으면 로그인된다', async ({ page, request }) => {
  const email = uniqueEmail();
  await createAccount(request, email);

  await page.goto('/login');
  await page.getByLabel('이메일').fill(email);
  await passwordField(page).fill(`${PASSWORD}-wrong`);
  await page.getByRole('button', { name: '로그인' }).click();

  await expect(page.getByRole('alert')).toHaveText(
    '이메일이나 비밀번호가 맞지 않아요. 다시 확인해 주세요.',
  );
  await expect(page).toHaveURL('/login');
  expect(await sessionCookie(page)).toBeUndefined();
  // 다시 시도할 수 있어야 한다.
  await expect(page.getByRole('button', { name: '로그인' })).toBeEnabled();

  await passwordField(page).fill(PASSWORD);
  await page.getByRole('button', { name: '로그인' }).click();

  await expect(page).toHaveURL('/');
  // 부를 이름 없이 만든 계정이라 이름 없이 인사한다.
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('안녕하세요');
});
