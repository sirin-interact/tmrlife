import { defineConfig, devices } from '@playwright/test';

// 서버와 DB를 띄우는 일은 이 설정이 아니라 저장소 루트의 `make e2e`가 맡는다.
// 빈 포트를 골라 띄우기 때문에 주소를 여기에 적어 둘 수 없고, 환경 변수로 받는다.
const baseURL = process.env.E2E_BASE_URL;
if (!baseURL) {
  throw new Error('E2E_BASE_URL이 없다. 저장소 루트에서 make e2e로 돌린다.');
}

// 사람들이 실제로 드는 폰은 두 엔진(Blink, WebKit)으로 갈린다. 대화 채널이 기대는 두 가지가 바로 그 경계에서 갈린다.
// 하나는 콘텐츠 보안 정책의 connect-src 'self'가 wss: 연결에도 적용되는지, 다른 하나는 WebSocket 손잡기에
// Sec-Fetch-* 머리글이 실리는지다(실리지 않으면 서버는 Origin과 Host를 견주는 길로 간다).
// 막히면 대화를 아예 시작하지 못하는데 서버 쪽에서는 아무 일도 없어 보인다. 그래서 눈으로는 잡히지 않는다.
// 다만 두 엔진을 늘 돌리면 시간이 배로 든다. 기본은 chromium 하나, 환경 변수를 주면 WebKit까지 돌린다.
const allBrowsers = process.env.PLAYWRIGHT_ALL_BROWSERS === '1';

export default defineConfig({
  testDir: './e2e',
  outputDir: './test-results',
  // 시나리오가 같은 서버의 시도 한도를 함께 쓴다. 차례대로 돌려서 서로의 결과에 끼어들지 않게 한다.
  workers: 1,
  fullyParallel: false,
  // 다시 돌려서 통과시키지 않는다. 가끔 실패하는 테스트는 고쳐야 할 테스트다.
  retries: 0,
  forbidOnly: Boolean(process.env.CI),
  timeout: 30_000,
  expect: { timeout: 10_000 },
  reporter: [['list'], ['html', { open: 'never', outputFolder: './playwright-report' }]],
  use: {
    baseURL,
    locale: 'ko-KR',
    timezoneId: 'Asia/Seoul',
    trace: 'retain-on-failure',
  },
  projects: [
    {
      // 폰에서 쓰는 앱이라 폰 크기의 화면으로 본다.
      name: 'chromium',
      use: { ...devices['Pixel 7'] },
    },
    // PLAYWRIGHT_ALL_BROWSERS=1일 때만 더한다. 데모 전에는 이 쪽도 돌려 본다.
    ...(allBrowsers ? [{ name: 'webkit', use: { ...devices['iPhone 14'] } }] : []),
  ],
});
