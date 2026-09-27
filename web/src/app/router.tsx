import { createBrowserRouter, type RouteObject } from 'react-router';

import { RouteErrorPage } from '@/app/ErrorBoundary';
import { GuestOnly, RequireAuth } from '@/auth/guards';
import { AppShell, type ShellHandle } from '@/components/AppShell';
import { Waiting } from '@/components/Waiting';
import { HelpPage } from '@/pages/HelpPage';
import { HomePage } from '@/pages/HomePage';
import { LegalPage } from '@/pages/LegalPage';
import { LoginPage } from '@/pages/LoginPage';
import { NotFoundPage } from '@/pages/NotFoundPage';
import { SignupPage } from '@/pages/SignupPage';

const wide: ShellHandle = { wide: true };
const talk: ShellHandle = { wide: true, immersive: true };
// 표 여섯 개와 그림 셋을 함께 보는 화면이다. 프로젝터로 띄울 때 폭을 반만 쓰지 않게 한다.
const review: ShellHandle = { wide: true, full: true };

// 처음 여는 화면은 로그인과 가입이다. 로그인한 뒤에야 쓰는 화면의 코드는 그 화면에 갈 때 받는다.
// 폰에서는 처음 받는 코드의 크기가 곧 첫 화면이 뜨는 속도다. 서비스 워커가 자리 잡은 뒤에는 모두 미리 받아 둔 것을 쓴다.
// "도움이 필요할 때"는 나눠 받지 않는다. 연결이 끊긴 순간에도 번호는 떠야 한다.
const lazyPage = {
  talk: async () => ({ Component: (await import('@/pages/TalkPage')).TalkPage }),
  diaryList: async () => ({ Component: (await import('@/pages/DiaryListPage')).DiaryListPage }),
  diary: async () => ({ Component: (await import('@/pages/DiaryPage')).DiaryPage }),
  evidence: async () => ({ Component: (await import('@/pages/EvidencePage')).EvidencePage }),
  trend: async () => ({ Component: (await import('@/pages/TrendPage')).TrendPage }),
  // 사용자 화면에서 이어지지 않는 화면이라 나머지 코드에 섞어 보내지 않는다.
  internalReview: async () => ({
    Component: (await import('@/pages/InternalReviewPage')).InternalReviewPage,
  }),
};

// 테스트가 같은 경로 정의를 메모리 라우터로 띄울 수 있게 라우터 생성과 분리해 둔다.
export const routes: RouteObject[] = [
  {
    element: <AppShell />,
    errorElement: <RouteErrorPage />,
    // 주소를 직접 열었는데 그 화면의 코드를 아직 받는 중일 때 잠깐 보인다. 앱 껍데기 밖이라 높이를 직접 준다.
    hydrateFallbackElement: <Waiting className="min-h-dvh" />,
    children: [
      // 새 화면은 기본으로 이 묶음에 넣는다. 로그인 없이 열려야 하는 화면만 아래에 따로 적는다.
      {
        element: <RequireAuth />,
        children: [
          { index: true, element: <HomePage /> },
          { path: 'talk', lazy: lazyPage.talk, handle: talk },
          { path: 'diary', lazy: lazyPage.diaryList, handle: wide },
          { path: 'diary/:date', lazy: lazyPage.diary, handle: wide },
          // 하루의 마음 신호와 그 근거. 추세 화면의 점 하나를 누르면 오는 자리다.
          { path: 'signals/:date', lazy: lazyPage.evidence, handle: wide },
          { path: 'trend', lazy: lazyPage.trend, handle: wide },
          // 계산이 그 값에 이른 과정. 화면 어디에서도 이어지지 않고, 볼 수 있는 계정인지는 서버가 판단한다.
          { path: 'internal/review', lazy: lazyPage.internalReview, handle: review },
        ],
      },
      {
        element: <GuestOnly />,
        children: [
          { path: 'login', element: <LoginPage /> },
          { path: 'signup', element: <SignupPage /> },
        ],
      },
      // 누구에게나 열려 있는 화면. 가장 힘든 순간과 가입하기 전에 읽는 글은 로그인 뒤에 숨기지 않는다.
      { path: 'help', element: <HelpPage />, handle: wide },
      { path: 'legal/:slug', element: <LegalPage />, handle: wide },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
];

export function createAppRouter() {
  return createBrowserRouter(routes);
}
