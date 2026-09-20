import '@testing-library/jest-dom/vitest';

import { cleanup } from '@testing-library/react';
import { afterEach, beforeEach, vi } from 'vitest';

// 전역 테스트 함수를 켜지 않아서 자동 정리가 걸리지 않는다. 테스트마다 직접 DOM을 비운다.
afterEach(() => {
  cleanup();
});

// 테스트가 실수로 진짜 네트워크에 닿지 않게 한다. 요청이 필요한 테스트는 mockApi로 응답을 정해 둔다.
beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL) => {
      const url = input instanceof Request ? input.url : String(input);
      return Promise.reject(new Error(`테스트가 응답을 정해 두지 않은 요청이다: ${url}`));
    }),
  );
});

// jsdom에는 ResizeObserver가 없다. 체크박스 기본 컴포넌트가 크기를 잴 때 쓰므로 빈 구현을 넣는다.
if (!('ResizeObserver' in globalThis)) {
  class ResizeObserverStub {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  }
  globalThis.ResizeObserver = ResizeObserverStub;
}
