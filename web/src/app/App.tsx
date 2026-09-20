import { useState } from 'react';
import { RouterProvider } from 'react-router/dom';

import { AppErrorBoundary } from '@/app/ErrorBoundary';
import { AppProviders } from '@/app/providers';
import { createAppRouter } from '@/app/router';
import { UpdatePrompt } from '@/components/UpdatePrompt';

export function App() {
  const [router] = useState(createAppRouter);

  return (
    <>
      <AppErrorBoundary>
        <AppProviders>
          <RouterProvider router={router} />
        </AppProviders>
      </AppErrorBoundary>
      {/* 서비스 워커 등록도 여기서 한다. 화면이 오류로 멈췄을 때야말로 새 버전을 받을 길이 열려 있어야 하므로 오류 경계 밖에 둔다. */}
      <UpdatePrompt />
    </>
  );
}
