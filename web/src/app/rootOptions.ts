import type { RootOptions } from 'react-dom/client';

function discard(): void {
  // 일부러 아무것도 하지 않는다.
}

/**
 * 리액트는 렌더링 중에 난 오류를 기본으로 브라우저 콘솔에 찍는다. 오류 경계가 잡은 것도 그렇다.
 * 오류 메시지에는 화면에 그리던 값이 섞일 수 있고, 이 앱이 그리는 값은 사용자의 대화와 일기다.
 * 콘솔을 거둬 가는 도구가 나중에 붙으면 그 글이 기기 밖으로 나간다. 그래서 오류 객체를 받아도 어디에도 남기지 않는다.
 * 사용자에게는 오류 경계가 안내 화면을 보여 준다.
 */
export const rootOptions: RootOptions = {
  onCaughtError: discard,
  onUncaughtError: discard,
  onRecoverableError: discard,
};
