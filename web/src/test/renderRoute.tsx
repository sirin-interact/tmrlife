import type { QueryClient } from '@tanstack/react-query';
import { render, type RenderResult } from '@testing-library/react';
import { createMemoryRouter, type InitialEntry, type RouteObject } from 'react-router';
import { RouterProvider } from 'react-router/dom';

import { AppProviders } from '@/app/providers';
import { routes } from '@/app/router';
import { createQueryClient } from '@/lib/queryClient';

type AppRouter = ReturnType<typeof createMemoryRouter>;

/**
 * 실제 경로 정의를 메모리 라우터로 띄워 주어진 주소의 화면을 그린다.
 * 어느 화면이든 로그인 여부부터 확인하므로, 부르기 전에 mockApi로 GET /api/v1/me의 응답을 정해 둔다.
 */
export function renderRoute(
  entry: InitialEntry,
  routeTable: RouteObject[] = routes,
): RenderResult & { router: AppRouter; queryClient: QueryClient } {
  const router = createMemoryRouter(routeTable, { initialEntries: [entry] });
  const queryClient = createQueryClient();
  // 실패한 조회를 한 번 더 해 보는 동작은 그대로 두고, 그 사이의 기다림만 없앤다.
  queryClient.setDefaultOptions({
    ...queryClient.getDefaultOptions(),
    queries: { ...queryClient.getDefaultOptions().queries, retryDelay: 0 },
  });

  return {
    router,
    queryClient,
    ...render(
      <AppProviders queryClient={queryClient}>
        <RouterProvider router={router} />
      </AppProviders>,
    ),
  };
}
