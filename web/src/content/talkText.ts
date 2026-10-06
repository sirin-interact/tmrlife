import type { ConnectionPhase, EndedInfo, TalkNotice } from '@/talk/conversationState';
import { USER_TEXT_MAX_LENGTH } from '@/talk/messages';

/** 대화 화면의 문구. 짧고 따뜻한 해요체로 쓴다. 화면 코드에는 문구를 따로 적지 않는다. */
export const TALK_TEXT = {
  title: '오늘의 이야기',
  speakerUser: '나',
  speakerAi: '내일',
  /** 화면 가운데에 보이는 지금의 한 마디(사용자의 말과 내일의 답) */
  captionLabel: '지금 나누는 말',
  /** 지난 말까지 모두 보는 자리. 여닫는 버튼의 이름이기도 하다. */
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

  /** 음성으로 이야기할 때. 마이크가 켜진 동안 맨 아래 줄에 보이는 말이다. */
  listening: '듣고 있어요',
  micStarting: '마이크를 켜는 중이에요',
  /** 글로 이야기하다가 음성으로 바꾸는 버튼 */
  voiceStart: '음성으로 이야기하기',
  /** 음성을 끄고 글로 이어가는 버튼 */
  voiceStop: '음성 끄기',
  /** 음성은 그대로 둔 채 글 쓰는 자리를 여는 버튼 */
  typeInstead: '글로 쓰기',
  /** 처음 들어왔을 때 구슬을 눌러 시작하게 권하는 말 */
  orbStartHint: '구슬을 누르면 이야기를 시작해요',
  orbStart: '이야기 시작하기',
  /** 듣는 동안 구슬을 누르면 말을 끝맺는다 */
  orbDone: '다 말했어요',
  /** 한 마디가 끝나 듣기를 멈춘 뒤, 구슬을 누르면 다음 말을 듣는다 */
  orbListen: '눌러서 말하기',
  /** 듣기를 멈춘 동안 맨 아래 줄에 보이는 말 */
  pausedHint: '구슬을 누르면 말할 수 있어요',
  /** 내일이 말하는 동안 구슬을 누르면 끊는다 */
  orbInterrupt: '잠깐 멈추기',
  /** 중간 자막이 화면 낭독기에 읽힐 때의 설명 */
  partialLabel: '듣는 중',

  /** 머리말이 없는 화면이라 "도움이 필요할 때"를 이 화면이 직접 둔다. 다른 화면의 머리말과 같은 말이어야 한다. */
  help: '도움이 필요할 때',
  /** 대화를 마치지 않고 화면을 나가는 길. 대화는 서버에 한동안 열려 있어서 돌아오면 이어진다. */
  back: '처음 화면으로',

  end: '끝내기',
  endConfirmTitle: '대화를 마칠까요?',
  endConfirmBody: '마친 뒤에도 언제든 다시 이야기할 수 있어요.',
  endConfirm: '네, 끝낼게요',
  endCancel: '계속 이야기할게요',
  ending: '대화를 마치고 있어요.',

  reconnect: '다시 연결하기',
  takeOver: '여기서 이어가기',
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

export const PHASE_TEXT: Record<ConnectionPhase, string | null> = {
  connecting: '대화를 준비하고 있어요.',
  ready: null,
  reconnecting: '연결이 잠시 끊겼어요. 다시 잇고 있어요.',
  failed: '연결이 이어지지 않아요. 인터넷 연결을 확인한 뒤 다시 시도해 주세요.',
  taken_over: '다른 화면에서 이야기를 이어가고 있어요. 여기서 이어가려면 아래를 눌러 주세요.',
  ended: null,
};

export function phaseText(phase: ConnectionPhase): string | null {
  return PHASE_TEXT[phase];
}

export const NOTICE_TEXT: Record<TalkNotice, string> = {
  send_failed: '방금 글을 보내지 못했어요. 글 아래의 "다시 보내기"를 눌러 주세요.',
  too_long: TALK_TEXT.tooLong,
  slow_down: '잠시 숨을 고르고 있어요. 곧 다시 보낼게요.',
  new_conversation: '한동안 말이 없어서 앞의 대화는 마무리됐어요. 새 대화로 이어갈게요.',
  protocol: '문제가 생겼어요. 화면을 새로 고친 뒤 다시 시도해 주세요.',
  voice_unavailable: '지금은 음성을 쓸 수 없어 글로 이어가요.',
  mic_denied:
    '마이크를 쓸 수 없어요. 브라우저 설정에서 마이크를 허용한 뒤 다시 눌러 주세요. 그동안은 글로 이야기할 수 있어요.',
  mic_missing: '마이크를 찾지 못했어요. 그동안은 글로 이야기할 수 있어요.',
  mic_insecure:
    '이 주소에서는 브라우저가 마이크를 열어 주지 않아요. https 주소나 설치한 앱에서 이야기해 주세요. 그동안은 글로 이야기할 수 있어요.',
  mic_failed: '마이크를 켜지 못했어요. 그동안은 글로 이야기할 수 있어요.',
  mic_silent:
    '마이크에서 소리가 들어오지 않아요. 기기의 소리 설정에서 쓰는 마이크를 골라 주세요. 그동안은 글로 이야기할 수 있어요.',
};

export function noticeText(notice: TalkNotice): string {
  return NOTICE_TEXT[notice];
}

export const ENDED_TEXT: Record<EndedInfo['reason'], string> = {
  user: '대화를 마쳤어요.',
  idle: '한동안 말이 없어서 대화를 마쳤어요.',
  elsewhere: '이 대화는 이미 마무리됐어요.',
  other: '대화를 마쳤어요.',
};

export function endedText(reason: EndedInfo['reason']): string {
  return ENDED_TEXT[reason];
}
