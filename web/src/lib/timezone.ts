const MAX_TIMEZONE_LENGTH = 64;

/**
 * 브라우저가 아는 지금 시간대의 이름(예: Asia/Seoul). 알 수 없으면 undefined다.
 * 가입할 때 한 번 보낸다. 서버는 이 값으로 하루의 경계와 알림 시각을 계산한다.
 */
export function browserTimeZone(): string | undefined {
  try {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    // 길이를 넘는 값을 보내면 요청 전체가 거부된다. 그럴 바에는 보내지 않고 서버의 기본값을 쓴다.
    return zone && zone.length <= MAX_TIMEZONE_LENGTH ? zone : undefined;
  } catch {
    return undefined;
  }
}
