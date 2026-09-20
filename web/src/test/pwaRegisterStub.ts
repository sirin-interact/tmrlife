import { useState } from 'react';
import { vi } from 'vitest';

// 'virtual:pwa-register/react'의 테스트용 대역. 실제 모듈과 같은 모양을 돌려준다.
interface RegisterOptions {
  onRegisteredSW?: (scriptUrl: string, registration: ServiceWorkerRegistration | undefined) => void;
}

/** 새 버전이 받아져 기다리고 있는 상태로 시작할지. 테스트가 그리기 전에 정한다. */
export const pwaRegisterStub = {
  needRefresh: false,
  updateServiceWorker: vi.fn<(reloadPage?: boolean) => Promise<void>>(() => Promise.resolve()),
  reset(): void {
    this.needRefresh = false;
    this.updateServiceWorker.mockClear();
  },
};

export function useRegisterSW(_options?: RegisterOptions) {
  const needRefresh = useState(pwaRegisterStub.needRefresh);
  const offlineReady = useState(false);
  return { needRefresh, offlineReady, updateServiceWorker: pwaRegisterStub.updateServiceWorker };
}
