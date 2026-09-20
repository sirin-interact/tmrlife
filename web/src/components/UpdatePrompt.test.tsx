import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it } from 'vitest';

import { UpdatePrompt } from '@/components/UpdatePrompt';
import { pwaRegisterStub } from '@/test/pwaRegisterStub';

beforeEach(() => {
  pwaRegisterStub.reset();
});

describe('UpdatePrompt', () => {
  it('새 버전이 없으면 아무것도 보여 주지 않는다', () => {
    const { container } = render(<UpdatePrompt />);

    expect(container).toBeEmptyDOMElement();
  });

  it('새 버전이 받아지면 알리고, 새로고침을 누르면 새 버전으로 바꾼다', async () => {
    pwaRegisterStub.needRefresh = true;
    render(<UpdatePrompt />);

    expect(screen.getByRole('status')).toHaveTextContent('새 버전이 준비됐어요');
    expect(pwaRegisterStub.updateServiceWorker).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole('button', { name: '새로고침' }));

    expect(pwaRegisterStub.updateServiceWorker).toHaveBeenCalledExactlyOnceWith(true);
  });

  it('나중에 하기를 누르면 새로 고치지 않고 알림만 닫는다', async () => {
    pwaRegisterStub.needRefresh = true;
    render(<UpdatePrompt />);

    await userEvent.click(screen.getByRole('button', { name: '나중에 하기' }));

    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(pwaRegisterStub.updateServiceWorker).not.toHaveBeenCalled();
  });
});
