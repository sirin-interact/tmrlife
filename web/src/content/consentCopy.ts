import type { ConsentKind } from '@/api/types';
import type { LegalSlug } from '@/content/legal/documents';

/**
 * 가입 화면의 동의 문구.
 *
 * !! 초안이다. 법무 검토를 거친 문구가 아니다(content/legal/status.ts). !!
 * 어떤 동의를 받아야 하는지와 그 판(version)은 서버가 정한다. 이 파일은 종류와 판마다 보여 줄 글을 맡는다.
 *
 * 문구는 판에 묶여 있다. 서버가 알려준 판이 여기 적힌 판과 다르면 이 앱은 그 동의에 보여 줄 글이 없는 것이고,
 * 가입 화면은 가입을 받지 않는다. 그래서 문구나 문서를 고치면 여기의 판과 서버의 판을 함께 올려야 하고,
 * 옛 글을 보고 한 동의가 새 글에 대한 동의로 기록되는 일이 생기지 않는다.
 */
export interface ConsentCopy {
  /** 이 글의 판. 서버가 알려준 판과 같아야 보여 준다. */
  version: string;
  /** 체크박스 옆에 놓이는 이름 */
  title: string;
  /** 무엇에 동의하는지 풀어 쓴 두세 문장 */
  description: string;
  /** 전문이 있는 문서와 그 안의 자리 */
  document: { slug: LegalSlug; sectionId?: string };
  /** 전문을 여는 링크의 이름. 항목마다 달라야 화면 낭독기로 링크만 훑을 때 구분된다. */
  linkLabel: string;
}

export const CONSENT_COPY: Record<ConsentKind, ConsentCopy> = {
  terms: {
    version: '2026-09-20',
    title: '서비스 이용약관',
    description: '내일을 쓰는 동안 서로 지킬 약속이에요.',
    document: { slug: 'terms' },
    linkLabel: '서비스 이용약관 읽기',
  },
  privacy: {
    version: '2026-09-20',
    title: '개인정보 처리방침',
    description:
      '이메일 주소, 로그인한 기기의 IP 주소와 브라우저 정보, 대화와 일기 같은 기록을 어떻게 받고 다루고 지우는지 담겨 있어요.',
    document: { slug: 'privacy' },
    linkLabel: '개인정보 처리방침 읽기',
  },
  sensitive_data: {
    version: '2026-09-20',
    title: '마음과 건강에 관한 민감정보 처리',
    // "돌아보는 데에만 쓴다"고 적지 않는다. 취소한 신호의 기록처럼, 내일이 말을 잘못 읽고 있지 않은지 점검하는 데에도 쓴다.
    description:
      '대화와 일기에는 마음과 건강에 관한 이야기가 담겨요. 이 기록은 내 마음의 변화를 돌아보고, 내일이 내 말을 잘못 읽고 있지 않은지 점검하는 데에만 써요. 다른 사람에게 전달하지 않아요.',
    document: { slug: 'privacy', sectionId: 'sensitive-data' },
    linkLabel: '민감정보 처리 안내 읽기',
  },
  overseas_transfer: {
    version: '2026-09-20',
    title: '외부 AI 서비스 이용과 국외 이전',
    description:
      '답을 만들고 일기를 정리하려고 대화 내용을 외부 AI 서비스(Google의 Gemini)로 보내요. 음성으로 이야기할 때는 말을 글로 옮기고 답을 소리로 바꾸려고 음성도 외부 AI 서비스로 보내요. 이때 미국 등 해외에 있는 서버에서 처리될 수 있어요. 동의하지 않으면 내일을 쓸 수 없어요.',
    document: { slug: 'privacy', sectionId: 'overseas-transfer' },
    linkLabel: '국외 이전 안내 읽기',
  },
};

export const CONSENT_TEXT = {
  groupLabel: '필수 동의',
  groupHint: '모두 동의해야 가입할 수 있어요',
  requiredMark: '(필수)',
  agreeSuffix: '에 동의해요',
  agreeAll: '아래 내용에 모두 동의해요',
  loading: '동의 항목을 불러오고 있어요.',
  draftNotice:
    '약관과 안내문은 아직 초안이에요. 정식 공개 전에 검토를 거치면서 바뀔 수 있고, 바뀌면 다시 동의를 받아요.',
  // 서버가 이 앱이 모르는 동의나 모르는 판을 요구할 때다. 설치된 앱이 옛 판이라 생기는 일이므로 새로 고침을 권한다.
  unknownKind:
    '가입에 필요한 안내를 모두 보여 드리지 못했어요. 화면을 새로 고친 뒤 다시 시도해 주세요.',
} as const;

/**
 * 서버가 요구한 동의에 보여 줄 글. 모르는 종류이거나 판이 다르면 undefined다.
 * 무엇에 동의하는지 보여 주지 못한 채로 동의를 받을 수는 없다.
 */
export function consentCopyFor(kind: string, version: string): ConsentCopy | undefined {
  if (!Object.hasOwn(CONSENT_COPY, kind)) return undefined;
  const copy = CONSENT_COPY[kind as ConsentKind];
  return copy.version === version ? copy : undefined;
}
