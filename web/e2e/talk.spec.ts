import {
  aiMessages,
  composer,
  CRISIS_REPLY,
  DIRECT_ASK,
  endTalk,
  expect,
  openTalk,
  OPENING,
  recordDateFromUrl,
  REFLECT_QUESTION,
  say,
  signUp,
  talkLog,
  test,
  uniqueEmail,
  userMessages,
} from './support.ts';

// 대화 채널(WebSocket)과 일기가 이어지는 길을 실제 서버로 밟아 본다.
// 언어 모델은 정해 둔 답으로 돈다(AI_PROVIDER=scripted). 그래서 나오는 말을 글자 그대로 견줄 수 있다.

test('대화를 나누고 마치면 일기 초안이 오고, 고쳐 저장한 뒤 일기장에서 찾을 수 있다', async ({
  page,
}) => {
  // 가입부터 일기 검색까지 한 흐름이다. 일기 초안은 다른 프로세스(작업자)가 만들어서 몇 초가 걸린다.
  test.slow();

  const toldAbout = '동네 도서관';
  const written = '미역국';

  await signUp(page, uniqueEmail(), '새벽');
  await openTalk(page);

  await test.step('세 번 이야기한다', async () => {
    await say(page, `오늘은 ${toldAbout}에 다녀왔어`);
    await say(page, '새로 나온 소설을 한 권 빌렸어');
    await say(page, '저녁에는 오랜만에 친구랑 통화했어');

    await expect(userMessages(page)).toHaveCount(3);
    // 첫 안부까지 네 마디다. 정해 둔 답은 짧은 존댓말 한두 문장이다.
    await expect(aiMessages(page)).toHaveCount(4);
  });

  await test.step('끝내면 일기 초안이 준비되고 그날의 일기로 넘어간다', async () => {
    await endTalk(page);
    await expect(page.getByRole('heading', { name: '대화를 마쳤어요.' })).toBeVisible();
    // 초안은 작업자가 만든다. 준비되면 대화 채널이 알려주고 화면이 그날의 일기로 넘어간다.
    await page.waitForURL(/\/diary\/\d{4}-\d{2}-\d{2}$/, { timeout: 60_000 });
    await expect(page.getByText('오늘의 일기 초안이 준비됐어요.')).toBeVisible();
  });

  const date = recordDateFromUrl(page.url());

  await test.step('초안을 고쳐 저장한다', async () => {
    const editor = page.getByLabel('일기', { exact: true });
    // 초안은 대화에서 한 말로 만들어진다. AI의 말은 재료가 아니다.
    await expect(editor).toHaveValue(new RegExp(toldAbout));
    await editor.fill(`${await editor.inputValue()}\n그리고 저녁에는 ${written}을 끓였다.`);
    await page.getByRole('button', { name: '저장', exact: true }).click();

    await expect(page.getByText('일기를 저장했어요.')).toBeVisible();
    // 저장하면 읽는 화면이 된다. 확인 전 표시는 사라진다.
    await expect(page.getByRole('button', { name: '고치기' })).toBeVisible();
  });

  await test.step('일기장에서 그 일기를 찾는다', async () => {
    await page.getByRole('link', { name: '일기장으로' }).click();
    await expect(page).toHaveURL(new RegExp(`/diary\\?month=${date.slice(0, 7)}$`));

    const entry = page.getByRole('link', { name: new RegExp(toldAbout) });
    await expect(entry).toHaveAttribute('href', `/diary/${date}`);
    await entry.click();
    await expect(page).toHaveURL(`/diary/${date}`);
    await expect(page.getByText(written)).toBeVisible();
  });

  await test.step('일기에 쓴 말로 찾는다', async () => {
    await page.getByRole('link', { name: '일기장으로' }).click();
    await page.getByLabel('일기에서 찾기').fill(written);
    await page.getByRole('button', { name: '찾기' }).click();

    await expect(page.getByText('1개의 일기를 찾았어요.')).toBeVisible();
    await expect(page.getByRole('link', { name: new RegExp(written) })).toHaveAttribute(
      'href',
      `/diary/${date}`,
    );
  });
});

test('죽고 싶다는 말에는 미리 써 둔 답과 전화번호가 나오고, 그 대화는 일기로 옮기지 않는다', async ({
  page,
}) => {
  await signUp(page, uniqueEmail());
  await openTalk(page);

  await test.step('미리 써 둔 답이 그대로 나온다', async () => {
    const answer = await say(page, '요즘 자꾸 죽고 싶다는 생각이 들어');
    expect(answer).toContain(CRISIS_REPLY);
    expect(answer).toContain('109');
  });

  const resources = page.getByRole('region', { name: '지금 바로 이야기할 수 있는 곳' });

  await test.step('전화번호가 화면에 고정된다', async () => {
    await expect(resources.getByRole('listitem')).toHaveCount(3);
    await expect(resources.getByRole('link', { name: /자살예방상담전화/ })).toHaveAttribute(
      'href',
      'tel:109',
    );
    await expect(resources.getByRole('link', { name: /정신건강위기상담/ })).toHaveAttribute(
      'href',
      'tel:15770199',
    );
    await expect(resources.getByRole('link', { name: /긴급 상황/ })).toHaveAttribute(
      'href',
      'tel:119',
    );
  });

  await test.step('화면을 새로 고쳐도 대화와 전화번호가 그대로 이어진다', async () => {
    await page.reload();
    await expect(talkLog(page).getByText(CRISIS_REPLY)).toBeVisible();
    await expect(userMessages(page)).toHaveCount(1);
    await expect(resources.getByRole('link', { name: /자살예방상담전화/ })).toHaveAttribute(
      'href',
      'tel:109',
    );
  });

  await test.step('마쳐도 일기 초안을 만들지 않는다', async () => {
    await endTalk(page);
    await expect(page.getByText('오늘 이야기는 여기까지예요. 와 주셔서 고마워요.')).toBeVisible();
    await expect(page.getByText('일기로 옮기고 있어요')).toHaveCount(0);

    await page.getByRole('link', { name: '일기장 보기' }).click();
    await expect(page.getByText('이 달에는 아직 일기가 없어요.')).toBeVisible();
  });
});

test('다른 화면이 대화를 이어받으면 앞의 화면은 물러나고, 사용자가 고를 때만 다시 가져온다', async ({
  page,
}) => {
  await signUp(page, uniqueEmail());
  await openTalk(page);
  await say(page, '오늘은 아침부터 비가 왔어');

  const second = await page.context().newPage();
  await second.goto('/talk');

  await test.step('새로 연 화면이 지난 발화와 함께 대화를 이어받는다', async () => {
    await expect(userMessages(second)).toHaveCount(1);
    await expect(aiMessages(second).first()).toContainText(OPENING);
  });

  await test.step('앞의 화면은 다시 잇지 않고 그대로 알린다', async () => {
    await expect(page.getByText('다른 화면에서 이야기를 이어가고 있어요.')).toBeVisible();
    // 여기서 저절로 다시 이으면 두 화면이 서로 대화를 끝없이 빼앗는다.
    await page.waitForTimeout(3_000);
    await expect(page.getByText('다른 화면에서 이야기를 이어가고 있어요.')).toBeVisible();
  });

  await test.step('사용자가 고르면 앞의 화면이 대화를 다시 가져온다', async () => {
    await page.getByRole('button', { name: '여기서 이어가기' }).click();
    await expect(userMessages(page)).toHaveCount(1);
    await say(page, '오후에는 갰어');
    await expect(second.getByText('다른 화면에서 이야기를 이어가고 있어요.')).toBeVisible();
  });

  await second.close();
});

test('여러 뜻으로 읽히는 말에는 되묻고, 그래도 풀리지 않으면 직접 묻고, 그 뒤에는 미리 써 둔 답이 나간다', async ({
  page,
}) => {
  await signUp(page, uniqueEmail());
  await openTalk(page);

  await test.step('먼저 그 말을 받아 되묻는다', async () => {
    const mirrored = await say(page, '그냥 사라지고 싶어');
    expect(mirrored).toContain('사라지고 싶어');
    expect(mirrored).toContain(REFLECT_QUESTION);
    // 되묻는 말에는 전화번호를 꺼내지 않는다.
    await expect(page.getByRole('region', { name: '지금 바로 이야기할 수 있는 곳' })).toHaveCount(
      0,
    );
  });

  await test.step('그래도 풀리지 않으면 돌려 말하지 않고 한 번 묻는다', async () => {
    const asked = await say(page, '다 그만하고 싶어');
    expect(asked).toContain(DIRECT_ASK);
  });

  await test.step('직접 물은 뒤에도 같은 말이 나오면 미리 써 둔 답이 나간다', async () => {
    const answer = await say(page, '그냥 다 사라졌으면 좋겠어');
    expect(answer).toContain(CRISIS_REPLY);
    await expect(
      page
        .getByRole('region', { name: '지금 바로 이야기할 수 있는 곳' })
        .getByRole('link', { name: /자살예방상담전화/ }),
    ).toHaveAttribute('href', 'tel:109');
  });

  await test.step('끝내기는 언제나 누를 수 있다', async () => {
    await endTalk(page);
    await expect(page.getByRole('heading', { name: '대화를 마쳤어요.' })).toBeVisible();
    await expect(composer(page)).toHaveCount(0);
  });
});
