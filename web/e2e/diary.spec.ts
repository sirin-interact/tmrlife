import {
  aiMessages,
  endTalk,
  expect,
  OPENING,
  openTalk,
  recordDateFromUrl,
  say,
  showTranscript,
  signUp,
  test,
  uniqueEmail,
  userMessages,
} from './support.ts';

test('하루를 지우면 일기장이 비고, 다시 들어가면 새 대화로 시작한다', async ({ page }) => {
  test.slow();

  const toldAbout = '한강 산책';

  await signUp(page, uniqueEmail());
  await openTalk(page);
  await say(page, `오늘은 ${toldAbout}을 했어`);
  await say(page, '바람이 시원해서 오래 걸었어');
  await endTalk(page);

  await page.waitForURL(/\/diary\/\d{4}-\d{2}-\d{2}$/, { timeout: 60_000 });
  const date = recordDateFromUrl(page.url());

  await test.step('초안을 저장해 둔다', async () => {
    await page.getByRole('button', { name: '저장', exact: true }).click();
    await expect(page.getByText('일기를 저장했어요.')).toBeVisible();
  });

  await test.step('무엇이 함께 지워지는지 알리고 한 번 더 확인받는다', async () => {
    await page.getByRole('button', { name: '이 날의 기록 지우기' }).click();
    const confirm = page.getByRole('alertdialog');
    await expect(confirm).toContainText('이 날 나눈 대화 전체');
    await expect(confirm).toContainText('지운 기록은 되돌릴 수 없어요.');
    await confirm.getByRole('button', { name: '모두 지우기' }).click();
  });

  await test.step('일기장이 빈다', async () => {
    await expect(page).toHaveURL(new RegExp(`/diary\\?month=${date.slice(0, 7)}$`));
    await expect(page.getByText('의 기록을 지웠어요.')).toBeVisible();
    await expect(page.getByText('이 달에는 아직 일기가 없어요.')).toBeVisible();
    await expect(page.getByRole('link', { name: new RegExp(toldAbout) })).toHaveCount(0);
  });

  await test.step('지운 날의 대화는 남지 않고, 새 대화가 열린다', async () => {
    await page.goto('/talk');
    await showTranscript(page);
    await expect(aiMessages(page)).toHaveCount(1);
    await expect(aiMessages(page).first()).toContainText(OPENING);
    await expect(userMessages(page)).toHaveCount(0);
  });

  await test.step('그날의 일기 화면은 다시 빈 날이 된다', async () => {
    await page.goto(`/diary/${date}`);
    await expect(page.getByRole('heading', { name: '이 날의 일기가 아직 없어요' })).toBeVisible();
  });
});
