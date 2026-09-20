import { Component, type ReactNode } from 'react';
import { isRouteErrorResponse, useRouteError } from 'react-router';

import { Button } from '@/components/ui/button';

interface ErrorScreenProps {
  title: string;
  description: string;
}

function ErrorScreen({ title, description }: ErrorScreenProps) {
  return (
    <main className="mx-auto flex min-h-dvh w-full max-w-content flex-col justify-center gap-6 px-gutter py-12">
      <div className="flex flex-col gap-3">
        <h1 className="text-2xl leading-snug font-semibold">{title}</h1>
        <p className="text-muted-foreground">{description}</p>
      </div>
      <Button asChild>
        <a href="/">처음으로 돌아가기</a>
      </Button>
    </main>
  );
}

/**
 * 라우터 안에서 난 오류를 받는 화면.
 * 오류 내용은 화면에도 콘솔에도 내보내지 않는다. 오류 메시지에 사용자가 쓴 글이 섞여 있을 수 있다.
 */
export function RouteErrorPage() {
  const error = useRouteError();

  if (isRouteErrorResponse(error) && error.status === 404) {
    return (
      <ErrorScreen
        title="페이지를 찾지 못했어요"
        description="주소가 바뀌었거나 없는 페이지예요. 처음 화면에서 다시 시작해 주세요."
      />
    );
  }

  return (
    <ErrorScreen
      title="잠시 문제가 생겼어요"
      description="불편을 드려 죄송해요. 잠시 뒤에 다시 시도해 주세요."
    />
  );
}

interface AppErrorBoundaryProps {
  children: ReactNode;
}

interface AppErrorBoundaryState {
  hasError: boolean;
}

/**
 * 라우터 바깥(프로바이더 등)에서 난 렌더링 오류를 받는 마지막 안전망.
 * 잡은 오류를 콘솔에 찍지 않게 하는 일은 루트 설정(rootOptions)이 맡는다.
 */
export class AppErrorBoundary extends Component<AppErrorBoundaryProps, AppErrorBoundaryState> {
  override state: AppErrorBoundaryState = { hasError: false };

  static getDerivedStateFromError(): AppErrorBoundaryState {
    return { hasError: true };
  }

  override render(): ReactNode {
    if (this.state.hasError) {
      return (
        <ErrorScreen
          title="잠시 문제가 생겼어요"
          description="불편을 드려 죄송해요. 잠시 뒤에 다시 시도해 주세요."
        />
      );
    }

    return this.props.children;
  }
}
