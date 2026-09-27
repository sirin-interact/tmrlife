import {
  DEMO_EMAIL,
  DEMO_PASSWORD,
  expect,
  logIn,
  MARK,
  signUp,
  test,
  trendCells,
  uniqueEmail,
} from './support.ts';

// 며칠에 걸친 점 달력과 시연 계정만 볼 수 있는 화면을 본다.
// 기록은 `make e2e`가 미리 심는다(server seed demo). 심는 명령은 신호 행을 써 넣지 않고 실제 추출 경로를 부르므로,
// 여기서 보는 점과 일수는 살아 있는 계산이 만든 값이다.
//
// 심는 이야기는 `server/cmd/server/seed.go`의 seedScript에 있다. 되풀이되는 하루가 아니라 흐름이고,
// 마지막 이야기가 언제나 오늘이다. 그래서 칸의 자리를 오늘 기준으로 세면 어느 날에 돌려도 같은 결과가 나온다.
// 창은 마지막 열넷이라 칸 번호 13이 오늘이고, 하루 앞이 12다.
// 심는 날 수(run.sh의 E2E_DEMO_DAYS)가 네 주라서, 평소를 잡는 앞 두 주와 이 창은 겹치지 않는다.
const TODAY = 13;
const YESTERDAY = 12;
/** 창의 첫날. 잠 이야기가 잦아지기 시작한 날이다. */
const WINDOW_START = 0;

test('심어 둔 기록의 점 달력은 세 줄에 그날의 상태를 그대로 그린다', async ({ page }) => {
  await logIn(page, DEMO_EMAIL, DEMO_PASSWORD);
  await page.goto('/trend');
  await expect(page.getByRole('heading', { level: 1, name: '변화 추세' })).toBeVisible();

  await test.step('줄은 기분, 수면, 에너지 셋이고 창은 열나흘이다', async () => {
    for (const row of ['기분', '수면', '에너지'] as const) {
      await expect(trendCells(page, row)).toHaveCount(14);
    }
    // 기록이 모자라다는 안내가 없다. 열나흘 내내 이야기했으니 창이 꽉 찼다.
    await expect(page.getByText('조금만 더 모아요')).toHaveCount(0);
    await expect(page.getByText('아직 기록이 없어요')).toHaveCount(0);
  });

  await test.step('칸의 세 가지 상태가 모두 그려진다', async () => {
    // 나빠지는 두 주라서 창 안에는 신호가 보인 날, 이야기가 나왔고 괜찮았던 날, 그 이야기가 없었던 날이 모두 있다.
    // 한 가지 상태만 나오면 점 읽는 법이 무엇을 가르치는지 화면에서 확인할 수 없다.
    const sleep = trendCells(page, '수면');
    await expect(sleep.nth(TODAY)).toContainText(MARK.observed);
    await expect(sleep.nth(WINDOW_START)).toContainText(MARK.observed);
    const mood = trendCells(page, '기분');
    await expect(mood.nth(TODAY)).toContainText(MARK.observed);
    // 창의 첫날은 아직 기분 이야기가 나빠지기 전이다.
    await expect(mood.nth(WINDOW_START)).toContainText(MARK.notObserved);
    // 에너지(피로) 줄에는 이야기가 나오지 않은 날이 섞여 있다.
    await expect(trendCells(page, '에너지').nth(YESTERDAY)).toContainText(MARK.observed);
  });

  await test.step('세 줄 모두 평소보다 잦다고 적는다', async () => {
    // 시연이 보여주겠다고 하는 것이 바로 이 말이다. 심는 기록의 앞 두 주와 이 창이 겹치면 셋 다 "비슷해요"가 된다.
    // 줄마다 "이야기한 열나흘 가운데 며칠"이 오고 견준 말이 뒤에 붙는다. 화면은 나누거나 세지 않는다.
    await expect(
      page.getByText(/^이야기한 14일 가운데 \d+일이에요\. 평소보다 잦아요\.$/),
    ).toHaveCount(3);
    await expect(page.getByText(/^평소에는 이야기한 14일 가운데 \d+일이었어요\.$/)).toHaveCount(3);
  });

  await test.step('점 읽는 법이 함께 있고, 점수와 단계는 없다', async () => {
    await expect(page.getByRole('heading', { name: '점 읽는 법' })).toBeVisible();
    for (const text of ['추정 점수', '개입 단계', '신뢰도']) {
      await expect(page.getByText(text)).toHaveCount(0);
    }
  });

  await test.step('날짜를 누르면 그날의 근거 화면으로 간다', async () => {
    const days = page.getByRole('link', { name: /월 \d+일/ });
    await days.nth(0).click();
    await expect(page).toHaveURL(/\/signals\/\d{4}-\d{2}-\d{2}$/);
  });
});

test('계산 살펴보기 화면은 시연 계정에만 열린다', async ({ page }) => {
  await test.step('보통 계정에는 거절을 조용히 알린다', async () => {
    await signUp(page, uniqueEmail());
    await page.goto('/internal/review');
    await expect(page.getByText('시연 계정에서만 볼 수 있어요')).toBeVisible();
    // 거절은 실패가 아니라 답이다. 놀라게 하는 알림이나 다시 시도하기 버튼을 두지 않는다.
    await expect(page.getByRole('alert')).toHaveCount(0);
    // 막힌 화면에서도 점수는 새어 나가지 않는다.
    await expect(page.getByText('합계')).toHaveCount(0);
    await expect(page.getByRole('link', { name: '처음 화면으로' })).toBeVisible();
  });

  await test.step('시연 계정에는 계산의 중간값까지 펼쳐 보여 준다', async () => {
    await logIn(page, DEMO_EMAIL, DEMO_PASSWORD);
    await page.goto('/internal/review');
    await expect(page.getByRole('heading', { level: 1, name: '계산 살펴보기' })).toBeVisible();

    // 숫자를 보기 전에 이 숫자가 무엇이 아닌지 먼저 읽힌다.
    await expect(page.getByRole('note')).toContainText('진단도 아니에요');

    for (const section of [
      '신호를 남긴 추출기',
      '추정 점수',
      '신뢰도',
      '평소',
      '변화 탐지',
      '개입 단계',
      '조정 값',
    ]) {
      await expect(page.getByRole('heading', { name: section, exact: true })).toBeVisible();
    }

    // 이 테스트는 정해 둔 답으로 돌기 때문에(AI_PROVIDER=scripted) 화면이 그 사실을 알려야 한다.
    // 키 없이 띄운 시연 기계도 똑같이 여덟 항목을 채우므로, 이 표시가 없으면 둘을 가릴 수 없다.
    await expect(page.getByText('모의 분석').first()).toBeVisible();
    await expect(page.getByText(/^정해 둔 답으로 채운 행이 \d+개 있어요\./)).toBeVisible();

    // 점수가 어떻게 나왔는지 항목마다 중간값이 함께 있다.
    const scoreTable = page.getByRole('table', { name: '항목별 점수', exact: true });
    await expect(scoreTable.getByRole('rowheader', { name: '수면' })).toBeVisible();
    await expect(scoreTable.getByRole('columnheader', { name: '환산 일수' })).toBeVisible();

    // 단계가 어떻게 움직였는지 날마다 적혀 있다.
    const stageTable = page.getByRole('table', { name: '날마다의 개입 단계', exact: true });
    await expect(stageTable.getByRole('columnheader', { name: '움직인 조건' })).toBeVisible();
    await expect(stageTable.getByRole('row')).not.toHaveCount(0);
  });
});
