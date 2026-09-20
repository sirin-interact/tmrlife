interface FieldErrorProps {
  /** 입력란의 aria-describedby가 가리키는 값 */
  id: string;
  message: string | undefined;
}

// role="alert"를 붙이지 않는다. 제출하면 오류 요약이 한 번 읽히고, 초점이 틀린 칸으로 가면서
// 이 문구가 그 칸의 설명으로 또 읽힌다. 여기까지 끼어들면 같은 말을 세 번 듣게 된다.
export function FieldError({ id, message }: FieldErrorProps) {
  if (!message) return null;

  return (
    <p id={id} className="text-sm leading-relaxed text-destructive">
      {message}
    </p>
  );
}
