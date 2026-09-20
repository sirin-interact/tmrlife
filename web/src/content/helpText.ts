import type { Resource } from '@/api/types';

/** "도움이 필요할 때" 화면의 문구 */
export const HELP_TEXT = {
  title: '도움이 필요할 때',
  lead: '혼자 견디기 어려운 순간에는 지금 바로 이야기를 들어줄 사람이 있어요. 번호를 누르면 바로 전화가 걸려요.',
  urgent: '몸이 위험하거나 한시가 급하면 망설이지 말고 119에 전화해 주세요.',
  someone: '믿을 수 있는 사람에게 지금 곁에 있어 달라고 말해 보는 것도 좋아요.',
  loading: '연락할 수 있는 곳을 불러오고 있어요.',
  fallbackNote: '목록을 새로 받아 오지 못해서 기본 번호를 보여 드려요.',
  documents: '내일의 약속',
} as const;

/**
 * 서버에서 목록을 받지 못했을 때 보여 주는 기본 번호.
 * 이 화면은 가장 힘든 순간에 여는 화면이다. 연결이 끊겼거나 서버가 멎었다고 빈 화면을 보여 줄 수는 없다.
 * 이름은 기관의 정식 이름이라 그대로 적는다. 서버의 목록과 같은 값으로 맞춰 둔다.
 */
export const FALLBACK_RESOURCES: readonly Resource[] = [
  {
    id: 'suicide_prevention_109',
    name: '자살예방상담전화',
    phone: '109',
    description: '24시간, 지금 바로 이야기를 들어줄 사람과 연결돼요.',
  },
  {
    id: 'emergency_119',
    name: '긴급 상황',
    phone: '119',
    description: '몸이 위험하거나 한시가 급할 때 바로 걸어요.',
  },
  {
    id: 'mental_health_crisis_1577_0199',
    name: '정신건강위기상담',
    phone: '1577-0199',
    description: '24시간, 마음이 위태로울 때 전화하면 가까운 지역의 도움과 이어 줘요.',
  },
];
