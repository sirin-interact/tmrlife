import { readFileSync } from 'node:fs';
import { fileURLToPath, URL } from 'node:url';

import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';
import { VitePWA } from 'vite-plugin-pwa';

import { pwaOptions } from './pwa.config.ts';
import { LEGAL_DOCUMENTS_ARE_DRAFT } from './src/content/legal/status.ts';

// 브라우저 흐름 테스트는 빈 포트에 서버를 띄우고 이 변수로 그 주소를 알려준다.
const API_TARGET = process.env.API_PROXY_TARGET ?? 'http://localhost:8080';

/**
 * 운영 웹 서버(Caddyfile)가 붙이는 콘텐츠 보안 정책을 그대로 읽어 온다.
 * 빌드 결과를 띄우는 미리보기 서버에 같은 정책을 붙여서, 브라우저 흐름 테스트가 운영과 같은 제약 아래에서 돈다.
 * 정책을 여기에 따로 적어 두면 두 값이 어긋나도 아무도 모른다. <style>을 끼워 넣는 라이브러리를 들였을 때
 * 테스트는 통과하고 운영에서만 화면이 깨지는 일을 막으려는 것이다.
 */
function productionContentSecurityPolicy(): string {
  const caddyfile = readFileSync(new URL('./Caddyfile', import.meta.url), 'utf8');
  const policy = /^\s*Content-Security-Policy\s+"([^"]+)"/m.exec(caddyfile)?.[1];
  if (policy === undefined) {
    throw new Error('Caddyfile에서 Content-Security-Policy를 찾지 못했다');
  }
  return policy;
}

export default defineConfig(({ command, isPreview }) => {
  // 검토를 마치지 않은 약관과 동의 문구로 정식 배포본을 만들지 않게 한다. 정식 배포본을 만드는 작업이 이 변수를 켠다.
  if (
    command === 'build' &&
    process.env.REQUIRE_REVIEWED_LEGAL === 'true' &&
    LEGAL_DOCUMENTS_ARE_DRAFT
  ) {
    throw new Error(
      '약관과 동의 문구가 아직 초안이다(src/content/legal/status.ts). 검토된 문구로 바꾼 뒤에 빌드한다.',
    );
  }

  return {
    plugins: [react(), tailwindcss(), VitePWA(pwaOptions)],
    resolve: {
      alias: {
        '@': fileURLToPath(new URL('./src', import.meta.url)),
      },
    },
    server: {
      port: 5173,
      strictPort: true,
      // changeOrigin을 켜지 않는다. Host 헤더가 그대로 전달되어야 서버의 같은 출처 확인이
      // 개발 환경에서도 운영 환경과 똑같이 동작한다.
      proxy: {
        '/api': { target: API_TARGET },
        '/ws': { target: API_TARGET, ws: true },
      },
    },
    // 빌드 결과를 띄울 때도 /api를 같은 출처로 넘긴다. 쿠키와 서버의 같은 출처 확인이 운영과 똑같이 동작한다.
    preview: {
      port: 4173,
      proxy: {
        '/api': { target: API_TARGET },
        '/ws': { target: API_TARGET, ws: true },
      },
      // 개발 서버에는 붙이지 않는다. 개발 서버는 고친 내용을 바로 비추려고 인라인 스크립트와 <style>을 쓴다.
      ...(isPreview && {
        headers: { 'Content-Security-Policy': productionContentSecurityPolicy() },
      }),
    },
  };
});
