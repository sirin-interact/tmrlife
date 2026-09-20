import { QueryClientProvider, type QueryClient } from '@tanstack/react-query';
import { useState, type ReactNode } from 'react';

import { createQueryClient } from '@/lib/queryClient';

interface AppProvidersProps {
  children: ReactNode;
  /** 테스트에서 격리된 클라이언트를 넣을 때만 쓴다. */
  queryClient?: QueryClient;
}

export function AppProviders({ children, queryClient }: AppProvidersProps) {
  const [client] = useState(() => queryClient ?? createQueryClient());

  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}
