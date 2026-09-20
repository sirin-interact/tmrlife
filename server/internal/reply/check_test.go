package reply_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/phrases"
	"github.com/sirin-interact/tmrlife/server/internal/reply"
)

func names(vs []reply.Violation) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.String())
	}
	return out
}

func check(d reply.Draft) []string {
	if d.Mode == "" {
		d.Mode = reply.ModeNormal
	}
	return names(reply.Check(d, reply.DefaultLimits()))
}

func TestCheck_Passes(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"반기고 하나를 묻는 답", "오, 재밌게 놀고 오셨나 봐요. 어디서 놀았어요?"},
		{"짧게 말할 자리를 여는 답", "무슨 일 있으셨어요?"},
		{"이유를 캐지 않고 공감으로 받는 답", "그런 날 있죠. 이유 없이 가라앉는 날엔 쉬는 게 제일이에요."},
		{"더 묻지 않고 마무리하는 답", "오늘 많이 지치셨나 봐요. 얘기는 여기까지 하고 푹 쉬어요."},
		{"뜻이 풀린 뒤의 답", "아, 그런 거였어요. 요즘 진짜 쉴 틈이 없었죠."},
		{"반어를 속뜻으로 받은 답", "진짜 엎친 데 덮친 격이었네요."},
		{"끊어 말한 감탄사는 문장으로 세지 않는다", "와! 축하해요! 어떤 회사예요?"},
		{"말을 고르는 줄임표도 문장으로 세지 않는다", "음... 그랬군요. 많이 놀랐겠어요."},
		{"쉼표로 이은 말은 한 문장이다", "그랬군요, 많이 놀랐겠어요. 지금은 좀 괜찮아요?"},
		{"소수점은 문장의 끝이 아니다", "학점이 3.5점이나 올랐네요. 기분이 어땠어요?"},
		{"요로 끝나는 이름씨에서는 문장을 끊지 않는다", "그럴 필요 없어요. 지금도 충분히 애쓰고 있어요."},
		{"물음표와 느낌표를 겹쳐 써도 질문은 하나다", "정말요?! 축하해요."},
		{"따옴표로 사용자의 말을 받아도 된다", "'그만하고 싶다'는 말이 마음에 남아요."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Empty(t, check(reply.Draft{Text: tc.text}))
		})
	}
}

func TestCheck_Length(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"빈 답", "  \n ", []string{"empty"}},
		{"세 문장", "아이고, 지갑을 잃어버리셨어요. 비까지 맞았으니 정말 속상했겠어요. 지금은 집에 잘 들어왔어요?", []string{"too_many_sentences:3"}},
		{"문장 부호 없이 이어 쓴 세 문장", "그랬군요 많이 힘드셨겠어요 오늘은 푹 쉬어요", []string{"too_many_sentences:3"}},
		{"두 문장이지만 너무 긴 답", strings.Repeat("오늘 하루 동안 있었던 여러 가지 일들 때문에 많이 지치고 힘드셨을 것 같은데 그래도 이렇게 이야기해 주셔서 ", 2) + "고마워요.", []string{"too_long"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, check(reply.Draft{Text: tc.text}))
		})
	}

	t.Run("한도는 조정할 수 있고 빈 값은 기본값으로 채운다", func(t *testing.T) {
		text := "그랬군요. 많이 놀랐겠어요."
		assert.Equal(t, []string{"too_many_sentences:2"}, names(reply.Check(reply.Draft{Mode: reply.ModeNormal, Text: text}, reply.Limits{MaxSentences: 1})))
		assert.Empty(t, reply.Check(reply.Draft{Mode: reply.ModeNormal, Text: text}, reply.Limits{}))
	})
}

func TestCheck_Questions(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		noQuestion bool
		mode       reply.Mode
		want       []string
	}{
		{"물음표가 둘이다", "오 재밌게 놀고 오셨어요? 어디서 놀았어요?", false, reply.ModeNormal, []string{"too_many_questions:2"}},
		{"물음표 없이 두 번 물었다", "무슨 일이 있었나요. 누구랑 있었던 건가요.", false, reply.ModeNormal, []string{"too_many_questions:2"}},
		{"질문 하나는 괜찮다", "어디서 놀았어요?", false, reply.ModeNormal, nil},
		{"묻지 않아야 하는 턴에 물었다", "어디서 놀았어요?", true, reply.ModeNormal, []string{"question_not_allowed"}},
		{"묻지 않아야 하는 턴에 앞 문장에서 물었다", "어디서 놀았어요? 재밌었겠네요.", true, reply.ModeNormal, []string{"question_not_allowed"}},
		{"묻지 않아야 하는 턴에 궁금하다고 에둘러 물었다", "어떤 일이 있었는지 궁금해요.", true, reply.ModeNormal, []string{"question_not_allowed"}},
		{"묻지 않아야 하는 턴에 알고 싶다고 에둘러 물었다", "무슨 일이었는지 알고 싶어요.", true, reply.ModeNormal, []string{"question_not_allowed"}},
		{"물음표 없는 ㄹ까요도 묻는 말이다", "같이 천천히 돌아볼까요.", true, reply.ModeNormal, []string{"question_not_allowed"}},
		{"물음표 없는 었나요도 묻는 말이다", "오늘 무슨 일 있었나요.", true, reply.ModeNormal, []string{"question_not_allowed"}},
		{"물음표 없는 ㄴ가요도 묻는 말이다", "지금은 어떤 기분이신가요.", true, reply.ModeNormal, []string{"question_not_allowed"}},
		{"물음표 없는 ㅂ니까도 묻는 말이다", "그렇습니까.", true, reply.ModeNormal, []string{"question_not_allowed"}},
		{"까닭을 대는 니까요는 묻는 말이 아니다", "많이 힘드셨을 테니까요.", true, reply.ModeNormal, nil},
		{"화나요는 묻는 말이 아니다", "그 얘기를 들으니 저도 화나요.", true, reply.ModeNormal, nil},
		{"가요는 묻는 말이 아니다", "시간이 참 빨리 가요.", true, reply.ModeNormal, nil},
		{"묻지 않아야 하는 턴에 반응만 했다", "오랜만에 기분 좋은 하루였네요.", true, reply.ModeNormal, nil},
		{"위기 응답 뒤의 대화에서도 연달아 묻지 않는다", "지금 곁에 누가 있어요?", true, reply.ModeCrisisFollow, []string{"question_not_allowed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := check(reply.Draft{Mode: tc.mode, Text: tc.text, NoQuestion: tc.noQuestion})
			assert.ElementsMatch(t, tc.want, got)
		})
	}
}

func TestCheck_PhoneNumbers(t *testing.T) {
	cases := []struct {
		name string
		text string
		user string
		want []string
	}{
		{"세 자리 번호", "지금 109에 걸어도 돼요.", "", []string{"phone_number:digits"}},
		{"번으로 끝나는 번호", "109번으로 걸면 돼요.", "", []string{"phone_number:digits"}},
		{"줄표가 든 번호", "1577-0199도 있어요.", "", []string{"phone_number:dashed", "phone_number:digits", "symbol:punctuation"}},
		{"지역 번호가 든 번호", "02-123-4567이에요.", "", []string{"phone_number:dashed", "phone_number:digits", "symbol:punctuation"}},
		{"한글로 읽은 번호", "일공구로 걸어도 돼요.", "", []string{"phone_number:spelled"}},
		{"한글로 읽은 119", "급하면 일일구예요.", "", []string{"phone_number:spelled"}},
		{"사용자가 먼저 말한 번호여도 걸린다", "109에 걸어볼 마음이 들었네요.", "109에 전화해 볼까", []string{"phone_number:digits"}},
		{"시각은 번호가 아니다", "새벽 3시까지 못 잤네요.", "", nil},
		{"금액은 번호가 아니다", "100만원이나 잃어버렸네요.", "", nil},
		{"날수는 번호가 아니다", "벌써 100일이 됐네요.", "", nil},
		{"이삼일은 번호를 읽은 말이 아니다", "이삼일 푹 쉬고 싶겠어요.", "", nil},
		{"일일이는 번호를 읽은 말이 아니다", "일일이 챙기느라 바빴겠어요.", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := reply.Draft{Text: tc.text}
			if tc.user != "" {
				d.UserTexts = []string{tc.user}
			}
			assert.ElementsMatch(t, tc.want, check(d))
		})
	}
}

func TestCheck_Referral(t *testing.T) {
	words := []struct {
		name string
		text string
		want string
	}{
		{"상담", "상담을 한번 받아 보면 어때요.", "referral:counsel"},
		{"전문가", "전문가와 이야기해 보면 좋겠어요.", "referral:expert"},
		{"전문의", "전문의를 만나 보는 방법도 있어요.", "referral:specialist"},
		{"의사 선생님", "의사 선생님께 말해 보면 어때요.", "referral:doctor"},
		{"센터", "가까운 센터에 가 보면 어때요.", "referral:center"},
		{"기관", "도와주는 기관이 있어요.", "referral:institution"},
		{"병원", "병원에 가 보면 어때요.", "referral:hospital"},
		{"정신과", "정신과에 가 보면 어때요.", "referral:psychiatry"},
		{"클리닉", "수면 클리닉도 있어요.", "referral:clinic"},
		{"보건소", "보건소에서도 봐 줘요.", "referral:health_office"},
		{"핫라인", "핫라인에 걸어 봐요.", "referral:hotline"},
		{"치료", "치료를 받으면 나아져요.", "referral:treatment"},
		{"진료", "진료를 받아 봐요.", "referral:medical_care"},
		{"처방", "처방을 받는 방법도 있어요.", "referral:prescription"},
		{"도움을 받으라는 말", "주변의 도움을 받아 보면 어때요.", "referral:seek_help"},
	}
	for _, tc := range words {
		t.Run("먼저 꺼내면 걸린다: "+tc.name, func(t *testing.T) {
			assert.Contains(t, check(reply.Draft{Text: tc.text}), tc.want)
		})
	}

	t.Run("사용자가 먼저 꺼낸 낱말을 받는 것은 걸리지 않는다", func(t *testing.T) {
		d := reply.Draft{Text: "상담은 어땠어요?", UserTexts: []string{"오늘 처음으로 상담 받고 왔어"}}
		assert.Empty(t, check(d))
	})

	t.Run("지난 턴에 사용자가 꺼낸 낱말도 받아도 된다", func(t *testing.T) {
		d := reply.Draft{Text: "병원 다녀온 뒤로는 좀 어때요?", UserTexts: []string{"어제 병원 갔다 왔어", "오늘은 그냥 집에 있었어"}}
		assert.Empty(t, check(d))
	})

	t.Run("사용자가 병원을 말했다고 정신과를 꺼내도 되는 것은 아니다", func(t *testing.T) {
		d := reply.Draft{Text: "정신과에 가 볼 생각도 했어요?", UserTexts: []string{"병원 가서 약 타올까 고민 중이야"}}
		assert.Equal(t, []string{"referral:psychiatry"}, check(d))
	})
}

func TestCheck_Tone(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"여쭤봐도 될까요", "하나 여쭤봐도 될까요?", []string{"permission_asking:may_i_ask"}},
		{"물어봐도 될까요", "무슨 일인지 물어봐도 될까요?", []string{"permission_asking:may_i_ask"}},
		{"질문 하나 드려도 될까요", "질문 하나 드려도 될까요?", []string{"permission_asking:may_i_ask"}},
		{"물어봐도 괜찮을까요", "혹시 하나만 물어봐도 괜찮을까요?", []string{"permission_asking:may_i_ask"}},
		{"괜찮으시다면", "괜찮으시다면 조금 더 들려주세요.", []string{"permission_asking:if_you_dont_mind"}},
		{"당신", "당신 잘못이 아니에요.", []string{"second_person"}},
		{"그냥 묻는 말은 걸리지 않는다", "무슨 일인지 물어보고 싶었어요.", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.ElementsMatch(t, tc.want, check(reply.Draft{Text: tc.text}))
		})
	}
}

func TestCheck_Diagnosis(t *testing.T) {
	cases := []struct {
		name string
		text string
		user string
		want []string
	}{
		{"병 이름을 먼저 꺼냈다", "우울증일 수도 있어요.", "", []string{"diagnosis:depression", "diagnosis:verdict"}},
		{"증상이라는 말", "그런 증상이 오래됐어요?", "", []string{"diagnosis:symptom"}},
		{"진단이라는 말", "진단을 받아 본 적 있어요?", "", []string{"diagnosis:diagnosis"}},
		{"불면증", "불면증이 있나 봐요.", "", []string{"diagnosis:insomnia"}},
		{"공황장애", "공황장애 같은 느낌이었어요?", "", []string{"diagnosis:panic_disorder"}},
		{"환자", "환자분들도 그래요.", "", []string{"diagnosis:patient"}},
		{"위험도", "위험도가 높아 보여요.", "", []string{"diagnosis:risk_level"}},
		{"비정상", "비정상은 아니에요.", "", []string{"diagnosis:abnormal"}},
		{"사용자가 꺼낸 병 이름을 판정 없이 받았다", "우울증이 맞는지 걱정될 만큼 요즘 힘들었네요.", "나 우울증인 것 같아. 우울증 맞지?", nil},
		{"사용자가 꺼낸 병 이름이어도 맞다고 하면 걸린다", "네, 우울증 맞는 것 같아요.", "나 우울증인 것 같아. 우울증 맞지?", []string{"diagnosis:verdict"}},
		{"사용자가 꺼낸 병 이름이어도 아니라고 하면 걸린다", "우울증은 아니에요. 그냥 지친 거예요.", "우울증 맞지?", []string{"diagnosis:verdict"}},
		{"그렇게 보인다고 해도 걸린다", "제가 보기엔 번아웃으로 보여요.", "요즘 너무 지쳐", []string{"diagnosis:verdict"}},
		{"사용자가 쓴 약어를 받는 것은 걸리지 않는다", "ADHD인가 싶을 만큼 집중이 안 됐네요.", "나 ADHD인가?", nil},
		{"사용자가 쓰지 않은 약어는 걸린다", "혹시 ADHD 얘기 들어 봤어요?", "요즘 집중이 안 돼", []string{"diagnosis:acronym"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := reply.Draft{Text: tc.text}
			if tc.user != "" {
				d.UserTexts = []string{tc.user}
			}
			assert.ElementsMatch(t, tc.want, check(d))
		})
	}
}

func TestCheck_Characters(t *testing.T) {
	cases := []struct {
		name string
		text string
		user string
		want []string
	}{
		{"이모지", "축하해요 😊", "", []string{"symbol:emoji"}},
		{"이어 붙인 이모지", "가족 얘기네요 👨‍👩‍👧", "", []string{"symbol:emoji"}},
		{"하트 기호", "응원해요 ♥", "", []string{"symbol:emoji"}},
		{"낱자모", "아이고 ㅠㅠ 속상했겠어요.", "", []string{"symbol:jamo"}},
		{"웃음 자모", "ㅋㅋ 재밌었겠네요.", "", []string{"symbol:jamo"}},
		{"물결표", "그랬군요~", "", []string{"symbol:punctuation"}},
		{"괄호", "그랬군요. (웃음)", "", []string{"symbol:punctuation"}},
		{"목록 기호", "* 오늘 있었던 일", "", []string{"symbol:punctuation"}},
		{"말 앞에 붙인 표시", "AI: 무슨 일 있었어요?", "", []string{"symbol:punctuation"}},
		{"줄바꿈", "그랬군요.\n많이 놀랐겠어요.", "", []string{"symbol:line_break"}},
		{"한자", "저는 하루 종일 사용跟你 이야기 나눌 준비를 했어요.", "", []string{"foreign_script:han"}},
		{"가나", "すごい 하루였네요.", "", []string{"foreign_script:hiragana"}},
		{"가타카나", "정말 ラッキー한 날이네요.", "", []string{"foreign_script:katakana"}},
		{"키릴 문자", "정말 хорошо한 하루였네요.", "", []string{"foreign_script:cyrillic"}},
		{"영어 문장", "That sounds tough. 많이 힘들었겠어요.", "", []string{"foreign_script:latin"}},
		{"영어로 미끄러진 낱말", "오늘 컨디션이 so so였나 봐요.", "", []string{"foreign_script:latin"}},
		{"전각 로마자", "ＯＫ예요.", "", []string{"foreign_script:latin"}},
		{"흔히 쓰는 대문자 약어는 괜찮다", "PPT 발표는 어땠어요?", "", nil},
		{"사용자가 쓴 영어 낱말은 그대로 받아도 된다", "feedback이 그렇게 길었어요?", "팀장이 feedback을 엄청 길게 줬어", nil},
		{"사용자가 쓴 낱말은 대소문자가 달라도 받아도 된다", "Feedback이 그렇게 길었어요?", "팀장이 feedback을 엄청 길게 줬어", nil},
		{"사용자가 쓰지 않은 긴 대문자 낱말은 걸린다", "NETFLIX 보셨어요?", "", []string{"foreign_script:latin"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := reply.Draft{Text: tc.text}
			if tc.user != "" {
				d.UserTexts = []string{tc.user}
			}
			assert.ElementsMatch(t, tc.want, check(d))
		})
	}
}

func TestCheck_CheckMode(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		words string
		users []string
		want  []string
	}{
		{"표현을 받아 하나를 물었다", "다 사라졌으면 싶을 만큼요. 오늘 무슨 일 있었어요?", "다 사라졌으면 좋겠어", nil, nil},
		{"말끝을 바꿔 받아도 받은 것이다", "사라지고 싶을 만큼 오늘 무슨 일이 있었어요?", "그냥 다 사라졌으면 좋겠어", nil, nil},
		{"묻지 않고 위로로 닫았다", "다 사라졌으면 싶을 만큼 힘들었네요. 오늘은 푹 쉬어요.", "다 사라졌으면 좋겠어", nil, []string{"missing_question"}},
		{"표현을 받지 않고 다른 말로 바꿨다", "많이 힘든 하루였나 봐요. 무슨 일 있었어요?", "다 사라졌으면 좋겠어", nil, []string{"not_mirrored"}},
		{"어느 말에나 있는 토막만 겹치면 받은 것이 아니다", "오늘 무슨 일 있었어요?", "오늘 그냥 다 끝났으면", nil, []string{"not_mirrored"}},
		{"말끝만 겹치면 받은 것이 아니다", "많이 힘드셨겠어요. 무슨 일 있었어요?", "다 사라졌으면 좋겠어", nil, []string{"not_mirrored"}},
		{"싶다는 말끝만 겹쳐도 받은 것이 아니다", "쉬고 싶어요? 무슨 일 있었어요?", "그냥 사라지고 싶어", nil, []string{"too_many_questions:2", "not_mirrored"}},
		{"받을 표현을 따로 주지 않으면 사용자의 마지막 말과 견준다", "그만하고 싶다는 건, 어떤 걸 그만하고 싶은 거예요?", "", []string{"오늘도 야근했어", "이제 그만하고 싶다"}, nil},
		{"되묻는 말에도 다른 검사는 그대로 걸린다", "사라지고 싶을 만큼요? 상담을 받아 보면 어때요?", "사라지고 싶어", nil, []string{"too_many_questions:2", "referral:counsel"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := check(reply.Draft{Mode: reply.ModeCheck, Text: tc.text, UserWords: tc.words, UserTexts: tc.users})
			assert.ElementsMatch(t, tc.want, got)
		})
	}

	t.Run("되물어야 하는 턴에는 직전에 두 번 물었어도 물어야 한다", func(t *testing.T) {
		d := reply.Draft{Mode: reply.ModeCheck, Text: "없어지고 싶을 만큼, 오늘 무슨 일이 있었어요?", UserWords: "그냥 없어지고 싶다", NoQuestion: true}
		assert.Empty(t, check(d))
	})
}

func TestCheck_ViolationsNeverCarryReplyText(t *testing.T) {
	texts := []string{
		"당신은 우울증 환자예요. 109에 전화하고 상담 받아 보세요 😊 ㅠㅠ~ 跟你 That is all. 물어봐도 될까요? 정말요? 그렇죠?",
		"",
		"그랬군요\n힘들었겠어요",
	}
	for _, text := range texts {
		for _, v := range reply.Check(reply.Draft{Mode: reply.ModeCheck, Text: text, UserWords: "사라지고 싶어", NoQuestion: true}, reply.DefaultLimits()) {
			assert.Regexp(t, `^[a-z_]+$`, string(v.Rule))
			assert.Regexp(t, `^[a-z0-9_]*$`, v.Detail, "로그에 그대로 남는 값이라 답의 글자가 들어가면 안 된다")
			assert.Equal(t, v.String(), v.LogValue().String())
		}
	}
}

func TestEndsWithQuestion(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"물음표로 끝난다", "오, 재밌게 놀고 오셨나 봐요. 어디서 놀았어요?", true},
		{"첫 안부는 질문이다", "오늘 하루는 어땠어요?", true},
		{"직접 묻는 말은 질문이다", "그런 생각이 들 만큼 힘들었네요. 혹시 죽고 싶다는 생각도 들어요?", true},
		{"물음표 없이 묻는 말로 끝난다", "오늘은 어떤 하루였나요", true},
		{"앞에서 묻고 반응으로 끝난다", "어디서 놀았어요? 재밌었겠네요.", false},
		{"반응으로 끝난다", "그런 날 있죠.", false},
		{"빈 말", " ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, reply.EndsWithQuestion(tc.text))
		})
	}
}

// 미리 써 둔 말은 모델의 답이 검사에 걸렸을 때 대신 나간다. 그 말이 같은 검사에 걸리면 앞뒤가 맞지 않는다.
func TestCheck_FallbackPhrasesPassTheSameRules(t *testing.T) {
	catalogue, err := phrases.Load()
	require.NoError(t, err)

	t.Run("안전한 말은 묻지 않아야 하는 턴에도 나갈 수 있다", func(t *testing.T) {
		first := catalogue.SafeReply("")
		for _, p := range []phrases.Phrase{first, catalogue.SafeReply(first.Display)} {
			for _, mode := range []reply.Mode{reply.ModeNormal, reply.ModeCrisisFollow} {
				assert.Empty(t, check(reply.Draft{Mode: mode, Text: p.Display, NoQuestion: true}), p.Display)
			}
		}
	})

	t.Run("되묻는 문형은 되물어야 하는 턴의 검사를 통과한다", func(t *testing.T) {
		for _, words := range []string{"다 사라졌으면 좋겠어", "이제 그만"} {
			p := catalogue.Reflect(words)
			assert.Empty(t, check(reply.Draft{Mode: reply.ModeCheck, Text: p.Display, UserWords: words}), p.Display)
		}
	})

	t.Run("직접 묻는 말과 말이 없을 때 묻는 말도 한두 문장에 질문 하나다", func(t *testing.T) {
		for _, p := range []phrases.Phrase{catalogue.DirectAsk(), catalogue.IdleCheck(), catalogue.Opening(), catalogue.MishearCheck()} {
			assert.Empty(t, check(reply.Draft{Text: p.Display}), p.Display)
		}
	})
}
