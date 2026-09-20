import type { ConnectionPhase, EndedInfo, TalkNotice } from '@/talk/conversationState';
import { USER_TEXT_MAX_LENGTH } from '@/talk/messages';

/** 대화 화면의 문구. 짧고 따뜻한 해요체로 쓴다. 화면 코드에는 문구를 따로 적지 않는다. */
export const TALK_TEXT = {
  title: '오늘의 이야기',
  speakerUser: '나',
  speakerAi: '내일',
  logLabel: '대화 내용',
  thinking: '내일이 답을 준비하고 있어요',
  inputLabel: '하고 싶은 이야기',
  inputPlaceholder: '편하게 이야기해 주세요',
  inputHint: 'Enter로 보내고, Shift+Enter로 줄을 바꿔요.',
  send: '보내기',
  tooLong: `한 번에 ${USER_TEXT_MAX_LENGTH.toLocaleString('ko-KR')}자까지 보낼 수 있어요. 나눠서 보내 주세요.`,
  sending: '보내는 중이에요',
  sendFailed: '보내지 못했어요.',
  resend: '다시 보내기',

  end: '끝내기',
  endConfirmTitle: '대화를 마칠까요?',
  endConfirmBody: '마친 뒤에도 언제든 다시 이야기할 수 있어요.',
  endConfirm: '네, 끝낼게요',
  endCancel: '계속 이야기할게요',
  ending: '대화를 마치고 있어요.',

  reconnect: '다시 연결하기',
  resourcesTitle: '지금 바로 이야기할 수 있는 곳',
  resourcesAnnounce: '지금 바로 이야기할 수 있는 곳의 전화번호를 화면 위쪽에 띄워 두었어요.',
  call: '전화하기',

  diaryWaiting: '오늘 이야기를 일기로 옮기고 있어요. 잠시만 기다려 주세요.',
  diarySlow: '일기를 정리하는 데 시간이 조금 더 걸리고 있어요. 준비되면 일기장에서 볼 수 있어요.',
  noDiary: '오늘 이야기는 여기까지예요. 와 주셔서 고마워요.',
  toDiary: '일기 보러 가기',
  toDiaryList: '일기장 보기',
  toHome: '처음으로',
  talkAgain: '다시 이야기하기',
} as const;

const PHASE_TEXT: Record<ConnectionPhase, string | null> = {
  connecting: '대화를 준비하고 있어요.',
  ready: null,
  reconnecting: '연결이 잠시 끊겼어요. 다시 잇고 있어요.',
  failed: '연결이 이어지지 않아요. 인터넷 연결을 확인한 뒤 다시 시도해 주세요.',
  ended: null,
};

export function phaseText(phase: ConnectionPhase): string | null {
  return PHASE_TEXT[phase];
}

const NOTICE_TEXT: Record<TalkNotice, string> = {
  send_failed: '방금 글을 보내지 못했어요. 글 아래의 "다시 보내기"를 눌러 주세요.',
  too_long: TALK_TEXT.tooLong,
  slow_down: '잠시 숨을 고르고 있어요. 곧 다시 보낼게요.',
  new_conversation: '한동안 말이 없어서 앞의 대화는 마무리됐어요. 새 대화로 이어갈게요.',
  protocol: '문제가 생겼어요. 화면을 새로 고친 뒤 다시 시도해 주세요.',
};

export function noticeText(notice: TalkNotice): string {
  return NOTICE_TEXT[notice];
}

const ENDED_TEXT: Record<EndedInfo['reason'], string> = {
  user: '대화를 마쳤어요.',
  idle: '한동안 말이 없어서 대화를 마쳤어요.',
  elsewhere: '이 대화는 이미 마무리됐어요.',
  other: '대화를 마쳤어요.',
};

export function endedText(reason: EndedInfo['reason']): string {
  return ENDED_TEXT[reason];
}
