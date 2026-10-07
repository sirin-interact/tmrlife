import type { VitePWAOptions } from 'vite-plugin-pwa';

// 설치 화면과 상태 표시줄 색은 밝은 테마의 배경 토큰(--background)과 같은 값이어야 한다.
// 값이 어긋나면 앱이 뜨는 순간 색이 튀어 보이므로, 토큰 테스트가 두 값을 함께 확인한다.
export const THEME_COLOR = '#f8f8f2';

export const pwaOptions: Partial<VitePWAOptions> = {
  // 대화 도중에 화면이 저절로 새로 고쳐지면 안 되므로 자동 갱신(autoUpdate)을 쓰지 않는다.
  // 새 버전은 받아 둔 채로 기다리고, 화면의 알림에서 사용자가 새로고침을 누르거나 앱을 다시 열 때 적용된다.
  registerType: 'prompt',
  // 등록은 알림 컴포넌트(UpdatePrompt)가 맡는다. 등록 코드를 따로 끼워 넣으면 두 번 등록된다.
  injectRegister: false,
  includeAssets: ['favicon.svg', 'icons/apple-touch-icon.png'],
  manifest: {
    id: '/',
    name: '내일',
    short_name: '내일',
    description: '오늘을 말하면, 내일이 보여요. 하루 5분, 말로 쓰는 마음 일기.',
    lang: 'ko',
    dir: 'ltr',
    start_url: '/',
    scope: '/',
    display: 'standalone',
    // 방향을 세로로 묶지 않는다. 거치대에 가로로 세워 둔 태블릿에서 설치한 앱이 옆으로 누워 버린다.
    orientation: 'any',
    theme_color: THEME_COLOR,
    background_color: THEME_COLOR,
    categories: ['lifestyle'],
    icons: [
      { src: '/icons/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
      { src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
      {
        src: '/icons/maskable-192.png',
        sizes: '192x192',
        type: 'image/png',
        purpose: 'maskable',
      },
      {
        src: '/icons/maskable-512.png',
        sizes: '512x512',
        type: 'image/png',
        purpose: 'maskable',
      },
    ],
  },
  workbox: {
    // 미리 캐시하는 것은 앱 껍데기(HTML, JS, CSS, 아이콘)뿐이다.
    // 글꼴은 90개가 넘는 조각 파일이라 미리 받지 않고, 실제로 쓰인 조각만 아래 규칙으로 캐시한다.
    globPatterns: ['**/*.{js,css,html,svg,png,ico,webmanifest}'],
    navigateFallback: '/index.html',
    // API와 WebSocket 경로는 서비스 워커가 앱 껍데기로 대신 답하지 않는다.
    // /healthz도 서버가 답하는 주소다. 앱을 설치해 둔 브라우저로 상태를 확인하러 들어갔을 때 앱 화면이 대신 뜨면
    // 서버가 멎었는지 알 수 없다.
    navigateFallbackDenylist: [/^\/api(?:\/|$)/, /^\/ws(?:\/|$)/, /^\/healthz$/],
    cleanupOutdatedCaches: true,
    // 응답에 개인 기록이 담기는 /api, /ws에는 런타임 캐시 규칙을 절대 추가하지 않는다.
    // 규칙이 없는 요청은 서비스 워커가 손대지 않고 브라우저가 평소대로 보낸다. 그래서 막는 규칙을 따로 두지 않는다.
    // 아래 규칙은 같은 출처의 글꼴 파일에만 해당한다. 규칙을 더하면 src/app/pwaConfig.test.ts가 /api, /ws에 닿는지 확인한다.
    runtimeCaching: [
      {
        // 이 함수는 글자 그대로 서비스 워커 파일에 옮겨진다. 바깥의 변수나 함수를 쓰면 거기서는 찾지 못한다.
        // 글꼴로 요청했더라도 /api, /ws의 응답은 담지 않는다. 정상적인 앱에서는 생기지 않는 요청이지만 길 자체를 막아 둔다.
        urlPattern: ({ request, sameOrigin, url }) =>
          sameOrigin &&
          request.destination === 'font' &&
          !/^\/(?:api|ws)(?:\/|$)/.test(url.pathname),
        handler: 'CacheFirst',
        options: {
          cacheName: 'fonts',
          expiration: { maxEntries: 120, maxAgeSeconds: 60 * 60 * 24 * 365 },
        },
      },
    ],
  },
};
