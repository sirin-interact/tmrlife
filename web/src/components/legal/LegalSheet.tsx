import { XIcon } from 'lucide-react';
import { useEffect, useId, useRef, useState } from 'react';
import { createPortal } from 'react-dom';

import { LegalDocumentView } from '@/components/legal/LegalDocumentView';
import { Button } from '@/components/ui/button';
import { LEGAL_DOCUMENTS, LEGAL_TEXT, type LegalSlug } from '@/content/legal/documents';

interface LegalSheetProps {
  slug: LegalSlug;
  sectionId?: string;
  onClose: () => void;
}

/**
 * 문서를 지금 화면 위에 겹쳐 띄운다. 가입 폼을 채우다가 약관을 읽으러 가도 쓰던 내용이 남아 있어야 한다.
 * 새 창으로 여는 방법은 설치된 앱에서 같은 창이 통째로 넘어가 버리는 기기가 있어서 믿을 수 없다.
 *
 * 대화 상자 라이브러리를 쓰지 않는 까닭: 뒤 화면의 스크롤을 막으려고 <style>을 끼워 넣는데, 운영 서버의 콘텐츠 보안 정책이 그것을 막는다.
 * 대신 화면 전체를 덮고, 뒤에 있는 것들을 inert로 꺼 둔다. 초점과 화면 낭독기가 뒤로 새지 않는다.
 */
export function LegalSheet({ slug, sectionId, onClose }: LegalSheetProps) {
  const titleId = useId();
  const sheetRef = useRef<HTMLDivElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  // 문서를 연 링크를 처음 그릴 때 잡아 둔다. 그린 뒤에는 문서가 초점을 가져가서 누가 열었는지 알 수 없다.
  const [opener] = useState(() =>
    document.activeElement instanceof HTMLElement ? document.activeElement : null,
  );

  useEffect(() => {
    const sheet = sheetRef.current;
    const behind = Array.from(document.body.children).filter(
      (element) => element !== sheet && !element.hasAttribute('inert'),
    );
    for (const element of behind) element.setAttribute('inert', '');
    // 가리키는 자리가 있으면 문서가 그 제목으로 초점을 옮긴다. 없을 때만 닫기 버튼에서 시작한다.
    if (sectionId === undefined) closeRef.current?.focus();

    return () => {
      for (const element of behind) element.removeAttribute('inert');
      // 닫으면 읽으러 오기 전의 자리(문서를 연 링크)로 돌아간다.
      opener?.focus();
    };
  }, [opener, sectionId]);

  useEffect(() => {
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', closeOnEscape);
    return () => document.removeEventListener('keydown', closeOnEscape);
  }, [onClose]);

  return createPortal(
    <div
      ref={sheetRef}
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      className="fixed inset-0 z-30 overflow-y-auto overscroll-contain bg-background"
    >
      <div className="mx-auto flex w-full max-w-content flex-col gap-6 px-gutter pt-[max(1rem,env(safe-area-inset-top))] pb-[max(2.5rem,env(safe-area-inset-bottom))] md:max-w-2xl">
        <div className="sticky top-0 -mx-2 flex justify-end bg-background py-2">
          {/* 가입 폼 안에서 띄운다. 리액트의 이벤트는 포털을 거슬러 올라가므로 제출 버튼으로 읽히지 않게 종류를 적어 둔다. */}
          <Button ref={closeRef} type="button" variant="ghost" onClick={onClose}>
            <XIcon aria-hidden="true" />
            {LEGAL_TEXT.close}
          </Button>
        </div>
        <LegalDocumentView
          document={LEGAL_DOCUMENTS[slug]}
          titleId={titleId}
          titleAs="h2"
          focusSectionId={sectionId}
        />
        <Button type="button" variant="outline" onClick={onClose}>
          {LEGAL_TEXT.close}
        </Button>
      </div>
    </div>,
    document.body,
  );
}
