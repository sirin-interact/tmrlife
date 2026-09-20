import { useState, type MouseEvent, type Ref } from 'react';

import type { ConsentGrant } from '@/api/types';
import { describedBy } from '@/components/form/describedBy';
import { FieldError } from '@/components/form/FieldError';
import { LegalSheet } from '@/components/legal/LegalSheet';
import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';
import { CONSENT_TEXT, consentCopyFor, type ConsentCopy } from '@/content/consentCopy';
import { legalPath } from '@/content/legal/documents';
import { LEGAL_DOCUMENTS_ARE_DRAFT } from '@/content/legal/status';

interface ConsentFieldsProps {
  /** 서버가 알려준, 가입에 필요한 동의들 */
  consents: readonly ConsentGrant[];
  /** 사용자가 동의한 종류들 */
  value: readonly string[];
  onChange: (next: string[]) => void;
  error: string | undefined;
  disabled?: boolean;
  /** 검사에 걸렸을 때 초점을 받을 곳 */
  focusRef?: Ref<HTMLButtonElement>;
}

const ERROR_ID = 'consents-error';

// 상자는 24px이고 라벨과의 사이에 틈을 두지 않는다. 상자의 누르는 영역을 사방으로 12px 넓혀 48px로 만들고,
// 라벨은 줄 전체 높이(48px 이상)를 차지하게 한다. 줄 어디를 눌러도 눌린다.
const CHECKBOX_HIT_AREA = "relative after:absolute after:-inset-3 after:content-['']";
const ROW_LABEL = 'min-h-touch flex-1 self-stretch py-3 pl-3';

/**
 * 동의는 항목마다 따로 받는다. 마음과 건강에 관한 기록을 다루는 일과 그 내용을 외부 AI 서비스로 보내는 일은
 * 약관 동의 한 번에 묻어가면 안 되고, 사용자가 각각 읽고 고를 수 있어야 한다.
 * "모두 동의"는 네 번 누르는 수고를 덜어 줄 뿐이다. 누른 뒤에도 항목마다 따로 끌 수 있다.
 */
export function ConsentFields({
  consents,
  value,
  onChange,
  error,
  disabled,
  focusRef,
}: ConsentFieldsProps) {
  const [openDocument, setOpenDocument] = useState<ConsentCopy['document'] | null>(null);

  // 보여 줄 글이 있는 동의만 받는다. 글이 없는 동의가 섞여 있으면 가입 화면이 가입 자체를 막는다.
  const shown = consents.flatMap(({ kind, version }) => {
    const copy = consentCopyFor(kind, version);
    return copy ? [{ kind, copy }] : [];
  });
  const kinds = shown.map(({ kind }) => kind);
  const allAgreed = kinds.length > 0 && kinds.every((kind) => value.includes(kind));

  function toggle(kind: string, checked: boolean) {
    const rest = value.filter((item) => item !== kind);
    onChange(checked ? [...rest, kind] : rest);
  }

  function openInPlace(event: MouseEvent<HTMLAnchorElement>, target: ConsentCopy['document']) {
    // 새 탭으로 열려고 누른 것(가운데 버튼, Ctrl, Cmd)은 브라우저에 맡긴다. 그 길로도 폼은 남는다.
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
      return;
    }
    event.preventDefault();
    setOpenDocument(target);
  }

  return (
    <fieldset className="flex flex-col gap-1" aria-describedby={describedBy(error && ERROR_ID)}>
      <legend className="mb-2 text-base font-medium">
        {CONSENT_TEXT.groupLabel}{' '}
        <span className="font-normal text-muted-foreground">({CONSENT_TEXT.groupHint})</span>
      </legend>

      {LEGAL_DOCUMENTS_ARE_DRAFT && (
        <p className="mb-1 text-sm leading-relaxed text-muted-foreground">
          {CONSENT_TEXT.draftNotice}
        </p>
      )}

      <div className="flex items-center border-b" data-consent-row="all">
        <Checkbox
          ref={focusRef}
          id="consent-all"
          checked={allAgreed}
          disabled={disabled}
          onCheckedChange={(checked) => onChange(checked === true ? [...kinds] : [])}
          className={CHECKBOX_HIT_AREA}
        />
        <Label htmlFor="consent-all" className={`${ROW_LABEL} font-semibold`}>
          {CONSENT_TEXT.agreeAll}
        </Label>
      </div>

      {shown.map(({ kind, copy }) => {
        const descriptionId = `consent-${kind}-description`;

        return (
          <div key={kind} className="flex flex-col">
            <div className="flex items-start" data-consent-row={kind}>
              <Checkbox
                id={`consent-${kind}`}
                required
                checked={value.includes(kind)}
                disabled={disabled}
                aria-invalid={error !== undefined && !value.includes(kind)}
                aria-describedby={descriptionId}
                onCheckedChange={(checked) => toggle(kind, checked === true)}
                // 첫 줄의 글자와 상자의 가운데를 맞춘다(라벨의 위쪽 여백 12px + 줄 높이와 상자 높이의 차이).
                className={`mt-3.5 ${CHECKBOX_HIT_AREA}`}
              />
              <Label
                htmlFor={`consent-${kind}`}
                className={`${ROW_LABEL} items-start leading-relaxed`}
              >
                <span>
                  <span className="text-muted-foreground">{CONSENT_TEXT.requiredMark}</span>{' '}
                  {copy.title}
                  {CONSENT_TEXT.agreeSuffix}
                </span>
              </Label>
            </div>
            {/* 설명과 링크는 라벨 밖에 둔다. 라벨 안에 있으면 체크박스의 이름이 길어지고, 링크를 누를 때 체크까지 바뀐다. */}
            <div className="flex flex-col items-start pb-2 pl-9">
              <p id={descriptionId} className="text-sm leading-relaxed text-muted-foreground">
                {copy.description}
              </p>
              <a
                href={legalPath(copy.document.slug, copy.document.sectionId)}
                target="_blank"
                rel="noopener"
                onClick={(event) => openInPlace(event, copy.document)}
                className="inline-flex min-h-touch items-center text-sm font-semibold text-link underline underline-offset-4"
              >
                {copy.linkLabel}
              </a>
            </div>
          </div>
        );
      })}

      <FieldError id={ERROR_ID} message={error} />

      {openDocument && (
        <LegalSheet
          slug={openDocument.slug}
          sectionId={openDocument.sectionId}
          onClose={() => setOpenDocument(null)}
        />
      )}
    </fieldset>
  );
}
