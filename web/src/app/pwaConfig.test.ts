import { describe, expect, it } from 'vitest';

import { pwaOptions } from '../../pwa.config.ts';

const ORIGIN = 'https://naeil.example';
const PRIVATE_PATHS = ['/api', '/api/v1/me', '/api/v1/auth/login', '/ws', '/ws/conversation'];
const DESTINATIONS: RequestDestination[] = ['', 'document', 'font', 'image', 'script', 'style'];

type RuntimeCaching = NonNullable<NonNullable<typeof pwaOptions.workbox>['runtimeCaching']>[number];

/** 서비스 워커가 규칙을 고를 때와 같은 방식으로, 요청이 그 규칙에 걸리는지 본다. */
function matches(rule: RuntimeCaching, path: string, destination: RequestDestination): boolean {
  const url = new URL(path, ORIGIN);
  const pattern = rule.urlPattern;

  if (typeof pattern === 'string') return new URL(pattern, ORIGIN).href === url.href;
  if (pattern instanceof RegExp) return pattern.test(url.href);

  const request = { url: url.href, destination } as Request;
  // 규칙은 event를 보지 않는다. 서비스 워커 밖에는 그 타입이 없어서 빈 값으로 채운다.
  return Boolean(pattern({ url, request, sameOrigin: true, event: undefined as never }));
}

describe('서비스 워커 설정', () => {
  const workbox = pwaOptions.workbox;

  it('개인 기록이 오가는 /api와 /ws는 어떤 런타임 캐시 규칙에도 걸리지 않는다', () => {
    const rules = workbox?.runtimeCaching ?? [];
    expect(rules.length).toBeGreaterThan(0);

    for (const rule of rules) {
      for (const path of PRIVATE_PATHS) {
        for (const destination of DESTINATIONS) {
          expect(
            matches(rule, path, destination),
            `${path} (${destination || 'fetch'}) 요청이 캐시 규칙에 걸린다`,
          ).toBe(false);
        }
      }
    }
  });

  it('위 확인이 실제로 규칙을 움직여 보고 있다 (글꼴 요청은 걸린다)', () => {
    const rules = workbox?.runtimeCaching ?? [];

    expect(rules.some((rule) => matches(rule, '/assets/pretendard.woff2', 'font'))).toBe(true);
  });

  it('/api와 /ws로 가는 이동에는 앱 껍데기로 대신 답하지 않는다', () => {
    const denylist = workbox?.navigateFallbackDenylist ?? [];

    for (const path of PRIVATE_PATHS) {
      expect(
        denylist.some((pattern) => pattern.test(path)),
        path,
      ).toBe(true);
    }
    expect(denylist.some((pattern) => pattern.test('/login'))).toBe(false);
    expect(denylist.some((pattern) => pattern.test('/apiary'))).toBe(false);
  });

  it('서버의 상태 확인 주소(/healthz)에도 앱 껍데기로 대신 답하지 않는다', () => {
    const denylist = workbox?.navigateFallbackDenylist ?? [];

    // 앱을 설치해 둔 브라우저로 상태를 확인하러 들어갔을 때 서버의 답 대신 앱 화면이 뜨면, 서버가 멎었는지 알 수 없다.
    expect(denylist.some((pattern) => pattern.test('/healthz'))).toBe(true);
    // 앞부분만 같은 화면 경로까지 막지는 않는다.
    expect(denylist.some((pattern) => pattern.test('/healthz-guide'))).toBe(false);
  });

  it('설치한 앱의 화면 방향을 묶지 않는다', () => {
    const manifest = pwaOptions.manifest;

    // 태블릿을 가로로 세워 두고 쓰는 사람이 있다.
    expect(manifest && manifest.orientation).toBe('any');
  });

  it('미리 받아 두는 것은 빌드된 정적 파일뿐이다', () => {
    expect(workbox?.globPatterns).toEqual(['**/*.{js,css,html,svg,png,ico,webmanifest}']);
    expect(workbox?.additionalManifestEntries).toBeUndefined();
  });

  it('새 버전은 저절로 적용되지 않고 사용자가 고를 때 적용된다', () => {
    expect(pwaOptions.registerType).toBe('prompt');
    expect(workbox?.skipWaiting).toBeUndefined();
    expect(workbox?.clientsClaim).toBeUndefined();
  });
});
