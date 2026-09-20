import { cn } from '@/lib/utils';

/** 고정 고지 문구. 글자 하나도 바꾸지 않는다. 문구가 필요한 곳에서는 늘 이 상수나 컴포넌트를 쓴다. */
export const MEDICAL_NOTICE_TEXT =
  '내일은 의료 서비스가 아니며, 제공되는 정보는 진단이나 치료를 대신하지 않습니다.';

interface MedicalNoticeProps {
  className?: string;
}

export function MedicalNotice({ className }: MedicalNoticeProps) {
  return (
    <p role="note" className={cn('text-sm leading-relaxed text-muted-foreground', className)}>
      {MEDICAL_NOTICE_TEXT}
    </p>
  );
}
