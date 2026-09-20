import { Link } from 'react-router';

import { Button } from '@/components/ui/button';

export function NotFoundPage() {
  return (
    <div className="flex flex-1 flex-col justify-center gap-6">
      <title>페이지를 찾지 못했어요 · 내일</title>
      <div className="flex flex-col gap-3">
        <h1 className="text-2xl leading-snug font-semibold">페이지를 찾지 못했어요</h1>
        <p className="text-muted-foreground">
          주소가 바뀌었거나 없는 페이지예요. 처음 화면에서 다시 시작해 주세요.
        </p>
      </div>
      <Button asChild>
        <Link to="/">처음으로 돌아가기</Link>
      </Button>
    </div>
  );
}
