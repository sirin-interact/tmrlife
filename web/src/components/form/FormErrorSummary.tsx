import { Alert, AlertDescription } from '@/components/ui/alert';

interface FormErrorSummaryProps {
  message: string | null;
  /** 칸마다의 오류 문구. 제출 버튼 근처에서 무엇을 고쳐야 하는지 한눈에 보이게 한다. */
  details?: readonly string[];
}

/**
 * 제출이 실패한 까닭을 한곳에 모아 보여 준다. 화면 낭독기는 나타나는 순간 읽어 준다.
 * 같은 문구로 다시 실패해도 읽히려면 부모가 제출할 때마다 key를 바꿔 새로 그려야 한다.
 */
export function FormErrorSummary({ message, details = [] }: FormErrorSummaryProps) {
  if (message === null) return null;

  // 요약 문구와 같은 말을 목록에서 한 번 더 하지 않는다.
  const items = [...new Set(details)].filter((detail) => detail !== message);

  return (
    <Alert variant="destructive">
      <AlertDescription>
        <p>{message}</p>
        {items.length > 0 && (
          <ul className="list-disc pl-5 text-sm">
            {items.map((item) => (
              <li key={item}>{item}</li>
            ))}
          </ul>
        )}
      </AlertDescription>
    </Alert>
  );
}
