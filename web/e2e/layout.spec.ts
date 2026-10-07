import { DEMO_EMAIL, DEMO_PASSWORD, expect, logIn, test } from './support';

test('작은 폰부터 데스크톱까지 주요 화면과 메뉴가 가로로 넘치지 않는다', async ({ page }) => {
  await logIn(page, DEMO_EMAIL, DEMO_PASSWORD);
  for (const width of [320, 390, 768, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    for (const path of ['/', '/diary', '/trend']) {
      await page.goto(path);
      await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
      await expect(page.getByRole('navigation', { name: '주요 메뉴' })).toBeVisible();
      await expect
        .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
        .toBe(true);
      for (const link of await page
        .getByRole('navigation', { name: '주요 메뉴' })
        .getByRole('link')
        .all()) {
        const box = await link.boundingBox();
        expect(box?.width).toBeGreaterThanOrEqual(44);
        expect(box?.height).toBeGreaterThanOrEqual(44);
      }
    }
  }
});

test('동작 줄이기를 존중하고 질문을 바꾸어도 주요 동작을 유지한다', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await logIn(page, DEMO_EMAIL, DEMO_PASSWORD);
  const prompt = page.getByRole('region', { name: '오늘을 꺼내는 작은 질문' });
  await expect(prompt).toContainText('오늘, 마음에 가장 오래 남은 순간은 무엇인가요?');
  await page.getByRole('button', { name: '다른 질문 보기' }).click();
  await expect(prompt).toContainText('오늘의 나에게 한마디를 건넨다면 뭐라고 할까요?');
  await expect(page.getByRole('link', { name: '오늘 이야기하기' })).toBeVisible();
  await expect
    .poll(() =>
      page
        .locator('.garden-sphere')
        .evaluate(
          (element) =>
            element.getAnimations().filter((animation) => animation.playState === 'running').length,
        ),
    )
    .toBe(0);
});
