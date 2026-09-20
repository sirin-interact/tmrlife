import { expect, test } from './support.ts';

test('도움이 필요할 때 화면은 로그인하지 않아도 열리고, 번호를 누르면 바로 걸린다', async ({
  page,
}) => {
  // 목록은 서버에서 온다. 받지 못했을 때 보여 주는 기본 번호와 같은 값이라 화면만 보고는 가릴 수 없다.
  const list = page.waitForResponse(
    (response) => new URL(response.url()).pathname === '/api/v1/resources',
  );

  await page.goto('/help');
  // 로그인 화면으로 보내지 않는다. 가장 힘든 순간에 여는 화면이다.
  await expect(page).toHaveURL('/help');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('도움이 필요할 때');

  const response = await list;
  expect(response.status()).toBe(200);

  // 급한 곳이 앞에 온다. 번호는 숫자만 남겨 tel:에 넣는다.
  const calls = page.getByRole('link', { name: /전화하기/ });
  await expect(calls).toHaveCount(3);
  await expect(calls.nth(0)).toHaveAttribute('href', 'tel:109');
  await expect(calls.nth(1)).toHaveAttribute('href', 'tel:119');
  await expect(calls.nth(2)).toHaveAttribute('href', 'tel:15770199');
  await expect(calls.nth(0)).toHaveAccessibleName('자살예방상담전화 109 전화하기');

  // 의료 서비스가 아니라는 고지와 약속 문서로 가는 길이 함께 있다.
  await expect(page.getByRole('note')).toContainText('의료 서비스가 아니며');
  await expect(page.getByRole('link', { name: '개인정보 처리방침' })).toBeVisible();
});
