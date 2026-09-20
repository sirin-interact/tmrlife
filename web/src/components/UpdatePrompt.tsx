import { XIcon } from 'lucide-react';
import { useRegisterSW } from 'virtual:pwa-register/react';

import { Button } from '@/components/ui/button';

// 설치된 앱은 닫지 않고 오래 열어 두기도 한다. 그동안에는 브라우저가 새 버전을 알아서 찾지 않는다.
const UPDATE_CHECK_INTERVAL_MS = 60 * 60 * 1000;

function checkForUpdatesPeriodically(registration: ServiceWorkerRegistration | undefined): void {
  if (!registration) return;
  window.setInterval(() => {
    // 연결이 끊긴 동안에는 확인해 봐야 실패만 쌓인다.
    if (!navigator.onLine) return;
    // 확인이 실패해도 할 일이 없다. 다음 차례에 다시 확인한다.
    registration.update().catch(() => undefined);
  }, UPDATE_CHECK_INTERVAL_MS);
}

/**
 * 서비스 워커를 등록하고, 새 버전이 받아지면 알려준다.
 * 새 버전을 곧바로 적용하지 않는다. 이야기하는 도중에 화면이 저절로 새로 고쳐지면 하던 말이 끊긴다.
 * 언제 바꿀지는 사용자가 고른다. 누르지 않으면 앱을 모두 닫았다가 다시 열 때 바뀐다.
 */
export function UpdatePrompt() {
  const {
    needRefresh: [needRefresh, setNeedRefresh],
    updateServiceWorker,
  } = useRegisterSW({
    onRegisteredSW: (_scriptUrl, registration) => checkForUpdatesPeriodically(registration),
  });

  if (!needRefresh) return null;

  return (
    <div
      role="status"
      className="fixed inset-x-0 bottom-0 z-20 px-gutter pb-[max(1rem,env(safe-area-inset-bottom))]"
    >
      <div className="mx-auto flex w-full max-w-content items-center gap-2 rounded-2xl border bg-card py-2 pr-2 pl-5 text-card-foreground shadow-lg">
        <p className="flex-1 font-medium">새 버전이 준비됐어요</p>
        <Button size="sm" onClick={() => void updateServiceWorker(true)}>
          새로고침
        </Button>
        <Button
          variant="ghost"
          size="icon"
          aria-label="나중에 하기"
          onClick={() => setNeedRefresh(false)}
        >
          <XIcon aria-hidden="true" />
        </Button>
      </div>
    </div>
  );
}
