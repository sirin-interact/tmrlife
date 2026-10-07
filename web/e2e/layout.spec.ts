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

test('처음 화면의 줄은 어디를 눌러도 그 화면으로 간다', async ({ page }) => {
  await logIn(page, DEMO_EMAIL, DEMO_PASSWORD);
  // 링크의 이름은 짧게 두고 누르는 자리만 줄 전체로 넓혔다. 글자가 없는 오른쪽 끝을 눌러도 가야 한다.
  const row = page.getByRole('listitem').filter({ hasText: '일기장 보기' });
  const box = await row.boundingBox();
  if (box === null) throw new Error('일기장 줄이 화면에 없다');
  await row.click({ position: { x: box.width - 24, y: box.height / 2 } });
  await expect(page).toHaveURL(/\/diary/);
});

test('동작 줄이기를 켜면 대화 화면의 구슬이 움직이지 않는다', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await logIn(page, DEMO_EMAIL, DEMO_PASSWORD);
  await page.getByRole('link', { name: '오늘 이야기하기' }).click();
  await expect(page).toHaveURL('/talk');
  const orb = page.locator('.orb');
  await expect(orb).toBeVisible();
  await expect
    .poll(() =>
      orb.evaluate(
        (element) =>
          element
            .getAnimations({ subtree: true })
            .filter((animation) => animation.playState === 'running').length,
      ),
    )
    .toBe(0);
});
