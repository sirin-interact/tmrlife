import { act } from '@testing-library/react';
import { createRoot } from 'react-dom/client';
import { describe, expect, it, vi } from 'vitest';

import { AppErrorBoundary } from '@/app/ErrorBoundary';
import { rootOptions } from '@/app/rootOptions';

const SECRET = '오늘 회사에서 있었던 일을 아무에게도 말 못 했어';

function Broken(): never {
  throw new Error(`rendering failed near: ${SECRET}`);
}

describe('렌더링 오류와 콘솔', () => {
  it('오류 경계가 잡은 오류의 내용을 콘솔 어디에도 찍지 않는다', async () => {
    const spies = (['error', 'warn', 'log', 'info', 'debug'] as const).map((level) =>
      vi.spyOn(console, level).mockImplementation(() => undefined),
    );
    const container = document.createElement('div');
    document.body.append(container);
    const root = createRoot(container, rootOptions);

    await act(async () => {
      root.render(
        <AppErrorBoundary>
          <Broken />
        </AppErrorBoundary>,
      );
      await Promise.resolve();
    });

    expect(container).toHaveTextContent('잠시 문제가 생겼어요');
    const printed = spies.flatMap((spy) => (spy.mock.calls as unknown[][]).flat()).map(String);
    expect(printed.join('\n')).not.toContain(SECRET);
    expect(printed.join('\n')).not.toContain('rendering failed');

    act(() => root.unmount());
    container.remove();
  });
});
