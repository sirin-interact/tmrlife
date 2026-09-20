import { cn } from '@/lib/utils';

interface WaitingProps {
  /** 화면 전체를 채워야 할 때(앱 껍데기 밖에서 보일 때) 높이를 넘겨 준다. */
  className?: string;
}

/**
 * 잠깐 기다리는 동안 보여 주는 화면.
 *
 * 대개는 눈 깜짝할 사이에 끝나므로 문구가 번쩍이면 오히려 거슬린다. 그래서 조금 기다렸다가 보여 준다
 * (appear-late). 화면 낭독기에는 바로 알린다.
 */
export function Waiting({ className }: WaitingProps) {
  return (
    <div role="status" className={cn('flex flex-1 items-center justify-center', className)}>
      <p className="animate-appear-late text-muted-foreground">잠시만 기다려 주세요.</p>
    </div>
  );
}
