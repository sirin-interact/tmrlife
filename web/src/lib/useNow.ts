import { useEffect, useState } from 'react';

// 화면을 켜 둔 채로 하루의 경계(새벽 4시)를 넘길 수 있다. 1분마다 다시 읽으면 날짜가 제때 바뀐다.
const REFRESH_INTERVAL_MS = 60_000;

/**
 * 지금 시각. 앱이 다시 화면에 보일 때와, 보이는 동안에는 1분마다 새로 읽는다.
 * 설치된 앱은 닫지 않고 며칠씩 열어 두기도 한다. 처음 그릴 때의 시각을 계속 쓰면 어제 날짜가 남아 있게 된다.
 */
export function useNow(): Date {
  const [now, setNow] = useState(() => new Date());

  useEffect(() => {
    function refresh() {
      if (document.visibilityState === 'visible') setNow(new Date());
    }
    document.addEventListener('visibilitychange', refresh);
    const timer = window.setInterval(refresh, REFRESH_INTERVAL_MS);
    return () => {
      document.removeEventListener('visibilitychange', refresh);
      window.clearInterval(timer);
    };
  }, []);

  return now;
}
