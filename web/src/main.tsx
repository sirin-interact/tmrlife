// 글꼴은 npm 패키지에서 가져와 같은 출처로 내보낸다. 외부 글꼴 CDN을 쓰면 접속 사실이 제3자에게 새어 나간다.
import 'pretendard/dist/web/variable/pretendardvariable-dynamic-subset.css';
import './index.css';

import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import { App } from '@/app/App';
import { rootOptions } from '@/app/rootOptions';

const container = document.getElementById('root');
if (!container) {
  throw new Error('root element not found');
}

createRoot(container, rootOptions).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
