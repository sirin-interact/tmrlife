import { defineConfig, devices } from '@playwright/test';

// 서버와 DB를 띄우는 일은 이 설정이 아니라 저장소 루트의 `make e2e`가 맡는다.
// 빈 포트를 골라 띄우기 때문에 주소를 여기에 적어 둘 수 없고, 환경 변수로 받는다.
const baseURL = process.env.E2E_BASE_URL;
if (!baseURL) {
  throw new Error('E2E_BASE_URL이 없다. 저장소 루트에서 make e2e로 돌린다.');
}

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
      // 폰에서 쓰는 앱이라 폰 크기의 화면으로 본다. 브라우저 엔진은 chromium 하나만 쓴다.
      name: 'chromium',
      use: { ...devices['Pixel 7'] },
    },
  ],
});
