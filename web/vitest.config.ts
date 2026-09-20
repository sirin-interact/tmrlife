import { fileURLToPath, URL } from 'node:url';

import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

const fromRoot = (path: string) => fileURLToPath(new URL(path, import.meta.url));

// 테스트에는 서비스 워커와 Tailwind 빌드가 필요 없어서 개발 서버 설정과 따로 둔다.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': fromRoot('./src'),
      // 서비스 워커 등록 모듈은 PWA 플러그인이 빌드할 때 만들어 주는 가상 모듈이다.
      // 테스트에는 그 플러그인이 없으므로, 테스트가 상태를 직접 움직일 수 있는 대역으로 바꿔 끼운다.
      'virtual:pwa-register/react': fromRoot('./src/test/pwaRegisterStub.ts'),
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    restoreMocks: true,
    unstubGlobals: true,
  },
});
