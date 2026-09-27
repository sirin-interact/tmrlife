import {
  endTalk,
  expect,
  openTalk,
  recordDateFromUrl,
  say,
  signalItems,
  signUp,
  test,
  uniqueEmail,
} from './support.ts';

// 심사에서 걸어 볼 길이다. 대화를 하나 하고, 그 대화에서 읽어 낸 신호와 근거를 보고,
// 아니라고 표시하면 추세의 일수가 줄고, 하루를 지우면 두 화면이 함께 빈다.
//
// 언어 모델은 정해 둔 답으로 돈다(AI_PROVIDER=scripted). 그 모델은 문맥을 보지 않고 낱말로 가리므로
// 실제 모델과 같은 판단을 낸다는 뜻이 아니다. 여기서 보는 것은 대화 → 추출 → 화면이 실제로 이어진다는 것과,
// 화면에 보이는 근거가 사용자가 한 말 그대로라는 것이다.

/** 잠 이야기가 나오는 말. 뽑은 근거가 이 문장 그대로여야 한다. */
const SAID_ABOUT_SLEEP = '어젯밤에 세 번 깼어';
/** 입맛 이야기가 나오고 괜찮았던 말 */
const SAID_ABOUT_APPETITE = '밥은 잘 먹었어';

test('대화에서 읽어 낸 신호와 근거를 보고, 아니라고 표시하면 추세에서 빠진다', async ({ page }) => {
  test.slow();

  await signUp(page, uniqueEmail());
  await openTalk(page);
  await say(page, SAID_ABOUT_SLEEP);
  await say(page, SAID_ABOUT_APPETITE);
  await endTalk(page);

  await page.waitForURL(/\/diary\/\d{4}-\d{2}-\d{2}$/, { timeout: 60_000 });
  const date = recordDateFromUrl(page.url());

  await test.step('근거 화면이 그날의 여덟 항목과 내가 한 말을 보여 준다', async () => {
    await page.goto(`/signals/${date}`);
    const items = signalItems(page);
    // 정리는 대화가 끝난 뒤에 작업자가 한다. 끝나지 않았으면 화면이 저절로 다시 물어본다.
    await expect(items).toBeVisible({ timeout: 60_000 });
    await expect(items.getByRole('listitem').filter({ hasText: /./ }).first()).toBeVisible();

    // 여덟 항목이 모두 있다. 이야기가 나오지 않은 항목도 빠지지 않는다.
    for (const label of [
      '즐거움',
      '기분',
      '잠',
      '기운',
      '입맛',
      '나를 보는 마음',
      '집중',
      '몸의 움직임',
    ]) {
      await expect(items.getByRole('heading', { name: label, exact: true })).toBeVisible();
    }

    // 근거는 손대지 않은 내 말이다. 줄이거나 다듬으면 이 확인이 깨진다.
    await expect(items).toContainText(SAID_ABOUT_SLEEP);
    await expect(items).toContainText(SAID_ABOUT_APPETITE);
    await expect(items).toContainText('잠이 편하지 않았어요');
    await expect(items).toContainText('잘 먹었어요');
    await expect(items).toContainText('직접 말한 내용');
    // 점수와 단계는 이 화면에 오지 않는다.
    await expect(page.getByText('추정 점수')).toHaveCount(0);
  });

  await test.step('추세 화면에 그날의 점이 찍힌다', async () => {
    await page.goto('/trend');
    await expect(page.getByRole('heading', { level: 1, name: '변화 추세' })).toBeVisible();
    // 이야기한 날이 하루뿐이라 평소와 견주지 않는다. 일수는 그대로 보여 준다.
    await expect(page.getByText('조금만 더 모아요')).toBeVisible();
    await expect(page.getByText('이야기한 1일 가운데 1일이에요.', { exact: true })).toHaveCount(1);
    await expect(page.getByText('이야기한 1일 가운데 0일이에요.', { exact: true })).toHaveCount(2);
  });

  // 여기서부터는 화면을 새로 읽지 않고 앱 안에서만 옮겨 다닌다.
  // 새로 읽으면 화면이 들고 있던 값이 함께 버려져서, 신호를 뺀 뒤에 추세 화면이 옛 점을 보여 주는 문제를
  // 이 테스트가 지나쳐 버린다. 심사에서 걸어 볼 길도 이쪽이다.
  await test.step('달력의 날짜를 눌러 근거 화면으로 가서 "이건 아니에요"를 누른다', async () => {
    await page
      .getByRole('link', { name: /월 \d+일/ })
      .first()
      .click();
    await expect(page).toHaveURL(`/signals/${date}`);
    await expect(signalItems(page)).toBeVisible({ timeout: 60_000 });

    await page.getByRole('button', { name: '잠: 이건 아니에요' }).click();
    await expect(
      page.getByText('이 신호는 변화 추세를 만드는 기록에서 빠져 있어요.'),
    ).toBeVisible();
    await expect(signalItems(page)).toContainText('빼 두었어요');
    // 무엇을 뺐는지 볼 수 있어야 한다. 행은 지우지 않는다.
    await expect(signalItems(page)).toContainText(SAID_ABOUT_SLEEP);
    await expect(page.getByRole('button', { name: '잠: 되돌리기' })).toBeVisible();
  });

  await test.step('추세로 돌아오면 일수가 하나 줄어 있다', async () => {
    await page.getByRole('link', { name: '변화 추세로' }).click();
    await expect(page).toHaveURL('/trend');
    await expect(page.getByText('이야기한 1일 가운데 1일이에요.', { exact: true })).toHaveCount(0);
    await expect(page.getByText('이야기한 1일 가운데 0일이에요.', { exact: true })).toHaveCount(3);
  });

  await test.step('되돌리면 다시 기록에 든다', async () => {
    await page
      .getByRole('link', { name: /월 \d+일/ })
      .first()
      .click();
    await expect(signalItems(page)).toBeVisible({ timeout: 60_000 });
    await page.getByRole('button', { name: '잠: 되돌리기' }).click();
    await expect(page.getByRole('button', { name: '잠: 이건 아니에요' })).toBeVisible();

    await page.getByRole('link', { name: '변화 추세로' }).click();
    await expect(page.getByText('이야기한 1일 가운데 1일이에요.', { exact: true })).toHaveCount(1);
  });

  await test.step('하루를 지우면 추세와 근거가 함께 빈다', async () => {
    await page.goto(`/diary/${date}`);
    await page.getByRole('button', { name: '이 날의 기록 지우기' }).click();
    const confirm = page.getByRole('alertdialog');
    await confirm.getByRole('button', { name: '모두 지우기' }).click();
    await expect(page).toHaveURL(new RegExp(`/diary\\?month=${date.slice(0, 7)}$`));

    // 지운 뒤에도 앱 안에서만 옮겨 다닌다. 지운 하루가 화면에 남아 있지 않은지 보려는 것이다.
    await page.getByRole('link', { name: '내일 처음 화면' }).click();
    await page.getByRole('link', { name: '변화 추세 보기' }).click();
    await expect(page).toHaveURL('/trend');
    await expect(page.getByText('아직 기록이 없어요')).toBeVisible();
    // 이야기한 날이 없으면 셀 것도 없다. "0일 가운데 0일이에요"는 셈이 아니라 고장처럼 읽힌다.
    await expect(page.getByText(/일 가운데 \d+일이에요\./)).toHaveCount(0);

    await page.goto(`/signals/${date}`);
    await expect(page.getByText('이 날은 이야기를 나누지 않았어요')).toBeVisible();
    await expect(signalItems(page)).toHaveCount(0);
    await expect(page.getByText(SAID_ABOUT_SLEEP)).toHaveCount(0);
  });
});
