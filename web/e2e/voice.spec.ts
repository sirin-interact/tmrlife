import type { Page } from '@playwright/test';

import {
  aiMessages,
  caption,
  composer,
  expect,
  OPENING,
  say,
  showTranscript,
  signUp,
  test,
  uniqueEmail,
  userMessages,
} from './support.ts';

// 음성 한 바퀴를 실제 공급자 없이 끝까지 밟아 본다.
// 브라우저의 마이크 자리에는 Web Audio로 만든 신호음 스트림을 끼운다. 서버의 가짜 인식기(VOICE_PROVIDER=fake)는
// 그 소리가 1초 쌓일 때마다 대본의 문장을 알아들은 것으로 치고, 답은 무음으로 읽어 준다.
// 캡처 워클릿, 소리 프레임, 자막, 턴, 재생, 방식 전환이 모두 이 길을 지난다. 가짜 인식기의 대본은 서버의 voice/fake 패키지와 같아야 한다.
const FIRST_HEARD = '오늘은 동네 도서관에 다녀왔어';

/**
 * 마이크를 신호음으로 바꿔 끼운다.
 *
 * 크로미움의 가짜 장치 플래그(--use-fake-device-for-media-stream)는 macOS에서 운영체제의 마이크 권한을 기다리다 영영 멈춘다.
 * 화면 없이 도는 브라우저는 그 권한 창을 띄울 수 없다. 그래서 운영체제를 거치지 않는 스트림을 돌려준다.
 * 실제 마이크를 여는 길(권한, 거절, 장치 없음)은 화면의 단위 테스트가 가짜 장치로 보고, 기기에서는 사람이 확인한다.
 */
async function useSyntheticMicrophone(page: Page): Promise<void> {
  await page.addInitScript(() => {
    navigator.mediaDevices.getUserMedia = async () => {
      const context = new AudioContext();
      const tone = context.createOscillator();
      tone.frequency.value = 440;
      const destination = context.createMediaStreamDestination();
      tone.connect(destination);
      tone.start();
      await context.resume();
      return destination.stream;
    };
  });
}

test('구슬을 누르면 마이크가 열리고, 알아들은 말로 답이 오고, 음성을 끄면 글로 이어진다', async ({
  page,
}) => {
  await useSyntheticMicrophone(page);
  await signUp(page, uniqueEmail());
  await page.getByRole('link', { name: '오늘 이야기하기' }).click();
  await expect(page).toHaveURL('/talk');
  await expect(caption(page)).toContainText(OPENING);

  await test.step('음성을 쓸 수 있으면 구슬이 시작 버튼이 된다', async () => {
    await expect(page.getByText('구슬을 누르면 이야기를 시작해요')).toBeVisible();
    // 구슬은 늘 숨 쉬듯 움직인다. 자리가 멈추기를 기다리는 검사는 끝나지 않으므로 건너뛴다. 사람의 손가락에는 문제가 없다.
    await page.getByRole('button', { name: '이야기 시작하기' }).click({ force: true });
    await expect(page.getByText('듣고 있어요')).toBeVisible();
  });

  await test.step('알아들은 말이 자막과 대화 내용에 오르고 답이 온다', async () => {
    // 끝점까지의 말은 사용자의 말로 올라오고, 답은 글로 먼저 닿고 소리로 뒤따른다.
    await expect(caption(page)).toContainText(FIRST_HEARD, { timeout: 15_000 });
    await showTranscript(page);
    await expect(userMessages(page).first()).toContainText(FIRST_HEARD);
    await expect.poll(() => aiMessages(page).count()).toBeGreaterThanOrEqual(2);
  });

  await test.step('음성을 끄면 글로 같은 대화를 이어간다', async () => {
    await page.getByRole('button', { name: '음성 끄기' }).click();
    await expect(composer(page)).toBeVisible();
    await expect(page.getByText('듣고 있어요')).toHaveCount(0);
    const before = await userMessages(page).count();
    const answer = await say(page, '글로도 이어서 써 볼게');
    expect(answer.length).toBeGreaterThan(0);
    await expect(userMessages(page)).toHaveCount(before + 1);
  });
});
