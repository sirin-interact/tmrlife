/** 일기장과 일기 화면의 문구. 화면 코드에는 문구를 따로 적지 않는다. */

/** 일기 글의 최대 길이(글자 수). 명세의 값과 같아야 한다. */
export const DIARY_MAX_LENGTH = 10_000;

export const DIARY_TEXT = {
  listTitle: '일기장',
  previousMonth: '이전 달',
  nextMonth: '다음 달',
  calendarLabel: '일기가 있는 날',
  weekdays: ['일', '월', '화', '수', '목', '금', '토'],
  hasDiary: '일기 있음',
  hasDraft: '확인 전 초안 있음',
  draftBadge: '확인 전',
  emptyMonth: '이 달에는 아직 일기가 없어요.',
  emptyFirstLine: '(아직 쓴 글이 없어요)',
  loading: '일기를 불러오고 있어요.',
  retry: '다시 불러오기',

  searchLabel: '일기에서 찾기',
  searchPlaceholder: '찾고 싶은 말',
  searchSubmit: '찾기',
  searchClear: '검색 지우기',
  searchEmpty: '찾는 말이 들어 있는 일기가 없어요.',
  searchBlank: '찾고 싶은 말을 입력해 주세요.',
  searchTooLong: '찾는 말은 100자까지 쓸 수 있어요.',
  searchResults: (count: number) => `${count}개의 일기를 찾았어요.`,

  draftReadyFromTalk: '오늘의 일기 초안이 준비됐어요.',
  draftIntro:
    '대화를 바탕으로 쓴 초안이에요. 읽어 보고 고친 뒤 저장해 주세요. 저장하기 전에는 확인 전으로 남아요.',
  draftAppendedIntro:
    '새로 나눈 이야기를 이어 붙인 초안이에요. 읽어 보고 고친 뒤 다시 저장해 주세요.',
  emptyDraftIntro: '초안을 만들지 못했어요. 오늘 하루를 직접 적어 주세요.',
  noDiaryTitle: '이 날의 일기가 아직 없어요',
  noDiaryBody: '대화를 나눈 날이라면 직접 써서 저장할 수 있어요.',
  writeOwn: '직접 쓰기',
  editorLabel: '일기',
  edit: '고치기',
  save: '저장',
  saving: '저장하고 있어요',
  cancelEdit: '그만두기',
  saved: '일기를 저장했어요.',
  editNote: '고치는 것은 일기 글뿐이에요. 대화에서 나온 마음 신호는 바뀌지 않아요.',
  textRequired: '일기 내용을 입력해 주세요.',
  textTooLong: `일기는 ${DIARY_MAX_LENGTH.toLocaleString('ko-KR')}자까지 쓸 수 있어요.`,
  outdated:
    '이 글을 열어 둔 사이에 새 초안이 도착했어요. 지금 저장하면 이 글이 일기가 되고, 새 초안은 사라져요.',
  reload: '새 초안 불러오기',
  noDayToWrite: '이 날에는 나눈 대화가 없어서 일기를 쓸 수 없어요.',
  confirmedAt: '확인한 일기',
  backToList: '일기장으로',

  deleteOpen: '이 날의 기록 지우기',
  deleteTitle: (date: string) => `${date}의 기록을 모두 지울까요?`,
  deleteItems: [
    '이 날의 일기',
    '이 날 나눈 대화 전체',
    '그 대화에서 읽어 낸 마음 신호와, 근거가 된 내 말',
    '이 날의 이야기에서 나온 기억',
  ],
  deleteLead: '일기만 지워지는 것이 아니에요. 아래 기록이 함께 지워져요.',
  deleteWarning: '지운 기록은 되돌릴 수 없어요. 변화 추세는 남은 기록으로 다시 계산돼요.',
  deleteConfirm: '모두 지우기',
  deleteCancel: '지우지 않기',
  deleted: (date: string) => `${date}의 기록을 지웠어요.`,
} as const;
