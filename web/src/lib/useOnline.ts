import { useSyncExternalStore } from 'react';

function subscribe(onChange: () => void): () => void {
  window.addEventListener('online', onChange);
  window.addEventListener('offline', onChange);
  return () => {
    window.removeEventListener('online', onChange);
    window.removeEventListener('offline', onChange);
  };
}

/**
 * 브라우저가 인터넷에 연결되어 있다고 보는지.
 * "연결됨"은 믿을 수 없는 값이다(와이파이에는 붙었지만 인터넷이 안 되는 경우). "끊김"일 때만 화면에 알린다.
 */
export function useOnline(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => navigator.onLine,
    () => true,
  );
}
