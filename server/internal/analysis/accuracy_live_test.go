package analysis_test

// 이 파일은 끝난 대화에서 신호를 얼마나 맞게 뽑는지 재는 계측 시험이다.
// 손으로 이름표를 붙인 대화 스물두 건(항목 판단 176개)을 실제 추출 경로로 세 번씩 돌려
// 항목별 정밀도와 재현율, 근거가 글자 그대로인 비율, 근거가 화면에서 끊긴 조각으로 읽히는 비율,
// 같은 대화를 다시 돌렸을 때의 흔들림, 걸린 시간을 한 번에 낸다.
// 같은 이름표로 정해 둔 답 모델(키 없이 뜬 서버가 쓰는 길)도 재서, 두 길의 숫자가 얼마나 다른지 나란히 남긴다.
//
// SIGNAL_ACCURACY_TESTS=1일 때만 돈다(실제 모델 쪽은 GEMINI_API_KEY도 있어야 한다). 모델을 예순여섯 번 부르므로
// 평소의 시험에 끼어들지 않게 따로 켜야 돌아간다. 항목별 표는 -v로, 근거 토막까지는 SIGNAL_ACCURACY_PRINT=1로 본다.
//
// # 이 숫자가 재지 않는 것
//
// 밖에 적을 때는 표본의 크기와 함께 적는다. 아래 셋은 이 시험이 재지 못하는 것이라, 숫자만 옮기면 과장이 된다.
//
//   - 대화는 모두 이 시험을 위해 지어낸 것이고, 이름표는 지시문이 적어 둔 규칙(남의 이야기, 관용 표현, 비꼼, 부인,
//     지난 일)을 그대로 적용해 붙였다. 그러므로 이 값은 "모델이 지시문을 따르는가"를 재고,
//     "지시문이 임상적으로 옳은가"와 "실제 음성 대화에서 얼마나 맞는가"는 재지 않는다.
//   - 항목별 양성 표본이 얇다. 관찰됨 이름표가 한 실행에 항목마다 두어 자리뿐인 항목이 있다.
//     항목별 재현율 1.000은 자리 몇 개에서 나온 값이므로 항목별로 단정하지 않는다. 전체 값만 밖에 적는다.
//   - 대화는 손으로 쓴 글이다. 받아쓴 말투를 흉내 낸 것이 한 건 있을 뿐, 실제 음성 인식 출력이 아니다.
//     말투도 반말 한 사람의 것뿐이다.
//
// 사람이 읽어도 갈리는 자리는 allow에 적어 채점에서 뺀다. 뺀 자리의 수도 함께 보고한다.

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai/scripted"
	"github.com/sirin-interact/tmrlife/server/internal/analysis"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
)

// accuracyRepeats는 같은 대화를 몇 번 돌려 볼지다. 판단이 실행마다 흔들리는 정도를 보려면 두 번으로는 모자란다.
const accuracyRepeats = 3

// 발표에 적을 초기 목표다.
const (
	targetPrecision = 0.8
	targetRecall    = 0.8
)

// maxDanglingQuoteRate는 이음말로 끝난 근거 토막이 차지해도 되는 최대 비율이다.
//
// 근거는 "왜 그렇게 봤는가"를 보여주려고 화면에 그대로 거는 글이다. 이음말로 끝난 토막은 말이 끊긴 조각으로 읽히고,
// 뒤에 올 말이 뜻을 뒤집는 자리라면 인용을 일부러 잘랐다고 읽힌다. 그래서 판단이 맞는 것과 따로 재고 따로 못박는다.
//
// 0은 될 수 없다. 이름표를 붙인 대화 가운데 세 건은 사용자의 발화 자체가 이음말로 끝나고, 지시문은 그런 줄을
// 그대로 옮기게 한다(사용자가 그렇게 말했으면 그렇게 옮긴다). 그 세 줄만으로 바닥이 약 0.06이다.
// 지시문에 자르는 규칙을 적기 전에는 0.195였고 적은 뒤에는 0.094였다. 0.15는 그 사이를 갈라, 실행마다 흔들리는 폭에는
// 걸리지 않고 옛 방식으로 돌아가면 걸리는 자리다.
const maxDanglingQuoteRate = 0.15

// danglingEndings는 뒤에 올 말이 있어야 문장이 되는 이음말의 끝 글자다.
// 다 적은 목록이 아니라 실제 근거에서 자주 나온 것만 적은 어림이다. 재는 값이 어림인 것을 알고 쓴다.
var danglingEndings = []string{
	"고", "서", "며", "데", "지만", "다가", "면서", "거나", "든지", "려고", "려면", "도록", "니까", "는지", "길래", "느라",
}

// endsDangling은 근거 토막이 이음말로 끝나 말이 끊겼는지 본다.
func endsDangling(quote string) bool {
	trimmed := strings.TrimRight(quote, " \t.,!?…·~\"'”’)]}")
	for _, ending := range danglingEndings {
		if strings.HasSuffix(trimmed, ending) {
			return true
		}
	}
	return false
}

// goldItem은 항목 하나에 붙인 이름표다.
type goldItem struct {
	// status는 사람이 읽고 정한 판단이다.
	status string
	// allow는 이것도 틀렸다고 하기 어려운 값들이다. 모델이 이 가운데 하나를 냈으면 채점에서 뺀다.
	allow []string
	// thirdParty는 그 항목의 신호가 대화에서 남의 것으로만 나왔다는 표시다. observed가 오면 남의 증상이 사용자에게 넘어온 것이다.
	thirdParty bool
	// needInQuote는 근거 토막에 적어도 하나는 들어 있어야 하는 글자다. 비꼼처럼 뒤에서 뜻이 뒤집히는 자리에 쓴다.
	// 앞토막만 잘라 오면 화면에는 판단과 반대로 읽히는 근거가 걸린다.
	needInQuote []string
}

func want(status string) goldItem { return goldItem{status: status} }

// maybe는 갈리는 자리다. status가 사람의 판단이고 alt도 받아들인다.
func maybe(status string, alt ...string) goldItem { return goldItem{status: status, allow: alt} }

// third는 그 항목이 남의 이야기로만 나왔다고 표시한다.
func third(g goldItem) goldItem {
	g.thirdParty = true
	return g
}

// needs는 근거 토막이 담아야 하는 글자를 적는다.
func needs(g goldItem, fragments ...string) goldItem {
	g.needInQuote = fragments
	return g
}

// accuracyPart는 대화 한 건과 그 대화의 이름표다.
type accuracyPart struct {
	label string
	turns []turn
	// gold는 여덟 항목을 모두 담아야 한다. 빠진 항목이 있으면 시험이 실패한다.
	gold map[signal.Item]goldItem
	// foreign은 어느 항목의 근거로도 쓰이면 안 되는 남의 말이다.
	// 사용자가 옮긴 남의 말은 사용자의 줄 안에 있어서 글자 대조로는 걸러지지 않는다.
	foreign []string
}

// accuracyCase는 이름표를 붙인 하루다. parts가 둘이면 하루에 두 번 대화한 날이다.
type accuracyCase struct {
	name  string
	parts []accuracyPart
}

var accuracyCases = []accuracyCase{
	{
		name: "누가 봐도 힘든 하루",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("오늘 하루는 어떻게 보내셨어요?"),
				user("아침부터 몸이 천근만근이었어. 이불에서 빠져나오는 데 한 시간 걸렸어"),
				assistant("많이 무거우셨네요. 회사에서는 어땠어요?"),
				user("모니터만 쳐다보다가 오후가 다 갔어. 보고서 한 줄도 못 썼어"),
				assistant("일이 손에 안 잡히는 날이었네요."),
				user("요즘은 다 내 탓인 것 같아. 기분도 하루 종일 축 가라앉아 있었어"),
				assistant("스스로한테 많이 엄격하시네요. 저녁은 드셨어요?"),
				user("저녁은 건너뛰었어. 먹고 싶은 생각이 안 들더라"),
				assistant("잠은 좀 어떠셨어요?"),
				user("새벽 세 시까지 뒤척이다가 겨우 눈 붙였어"),
			},
			gold: map[signal.Item]goldItem{
				signal.Interest:      want(statusNotMentioned),
				signal.Mood:          want(statusObserved),
				signal.Sleep:         want(statusObserved),
				signal.Fatigue:       want(statusObserved),
				signal.Appetite:      want(statusObserved),
				signal.SelfBlame:     want(statusObserved),
				signal.Concentration: want(statusObserved),
				// "이불에서 빠져나오는 데 한 시간"을 느려짐으로 읽을 수도 있어 갈리는 자리로 둔다.
				signal.Psychomotor: maybe(statusNotMentioned, statusObserved),
			},
		}},
	},
	{
		name: "누가 봐도 괜찮은 하루",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("오늘 하루는 어땠어요?"),
				user("오랜만에 푹 잤어. 알람 울리기 전에 저절로 눈이 떠졌는데 개운했어"),
				assistant("잘 쉬셨네요. 낮에는 어떻게 보내셨어요?"),
				user("기운이 넘쳐서 점심 먹고 회사 근처 공원까지 걸었어. 기분도 며칠 만에 좋았어"),
				assistant("좋은 산책이었겠어요."),
				user("기타도 다시 꺼냈어. 두 달 만인데 생각보다 재미있더라"),
				assistant("반가운 시간이었겠네요. 일은 어땠어요?"),
				user("일도 집중이 잘 됐어. 밀린 메일 다 정리했어"),
				assistant("식사는 어떻게 하셨어요?"),
				user("세 끼 다 챙겨 먹었고 저녁에는 떡볶이도 먹었어"),
			},
			gold: map[signal.Item]goldItem{
				signal.Interest:      want(statusNotObserved),
				signal.Mood:          want(statusNotObserved),
				signal.Sleep:         want(statusNotObserved),
				signal.Fatigue:       want(statusNotObserved),
				signal.Appetite:      want(statusNotObserved),
				signal.SelfBlame:     want(statusNotMentioned),
				signal.Concentration: want(statusNotObserved),
				signal.Psychomotor:   want(statusNotMentioned),
			},
		}},
	},
	{
		name: "짧게만 답하는 사람",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("어젯밤에 잠은 잘 오셨어요?"),
				user("아니"),
				assistant("자다가 깨기도 하셨어요?"),
				user("세 번쯤"),
				assistant("낮에 기운은 어땠어요?"),
				user("없었어"),
				assistant("밥은 잘 드셨어요?"),
				user("응"),
				assistant("요즘 재미있는 일은 있으세요?"),
				user("딱히"),
				assistant("오늘 이야기해 주셔서 고마워요."),
				user("어"),
			},
			gold: map[signal.Item]goldItem{
				signal.Interest:      want(statusObserved),
				signal.Mood:          want(statusNotMentioned),
				signal.Sleep:         want(statusObserved),
				signal.Fatigue:       want(statusObserved),
				signal.Appetite:      want(statusNotObserved),
				signal.SelfBlame:     want(statusNotMentioned),
				signal.Concentration: want(statusNotMentioned),
				signal.Psychomotor:   want(statusNotMentioned),
			},
		}},
	},
	{
		name: "남의 증상을 그대로 옮긴 하루",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("오늘 하루는 어땠어요?"),
				user("엄마가 요즘 밥맛이 통 없다고 전화가 와서 한참 들었어"),
				assistant("걱정되셨겠어요."),
				user("엄마가 입맛도 없고 밤에 잠도 안 온다고 하시더라"),
				assistant("어머니가 많이 힘드시네요. 본인은 오늘 어떠셨어요?"),
				user("나는 별일 없었어. 밥도 제때 먹었고 어젯밤에 잠도 잘 잤어"),
				assistant("다행이에요."),
				user("통화 끝나고 마음이 좀 무겁긴 했어"),
			},
			gold: map[signal.Item]goldItem{
				signal.Interest: want(statusNotMentioned),
				// 남의 일을 듣고 사용자가 느낀 감정은 사용자의 것이다.
				signal.Mood:          want(statusObserved),
				signal.Sleep:         third(want(statusNotObserved)),
				signal.Fatigue:       want(statusNotMentioned),
				signal.Appetite:      third(want(statusNotObserved)),
				signal.SelfBlame:     want(statusNotMentioned),
				signal.Concentration: want(statusNotMentioned),
				signal.Psychomotor:   want(statusNotMentioned),
			},
			foreign: []string{"밥맛이 통 없다", "입맛도 없고 밤에 잠도 안 온다"},
		}},
	},
	{
		name: "관용 표현과 과장이 이어지는 하루",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("오늘 하루는 어땠어요?"),
				user("아침에 지하철을 놓쳐서 뛰다가 진짜 죽는 줄 알았어"),
				assistant("많이 급하셨네요."),
				user("점심에는 배고파 죽겠어서 국수를 두 그릇이나 비웠어"),
				assistant("맛있게 드셨네요."),
				user("오후 회의는 지루해 죽겠더라. 그래도 끝나고 팀원들이랑 커피 마시면서 한참 웃었어"),
				assistant("좋은 마무리였네요."),
				user("어제 본 영화는 웃겨 죽는 줄 알았어"),
			},
			// 어느 항목도 관찰됨이 아니다. 두 그릇을 비웠으니 식욕은 오히려 괜찮았다.
			gold: map[signal.Item]goldItem{
				signal.Interest:      maybe(statusNotMentioned, statusNotObserved),
				signal.Mood:          maybe(statusNotMentioned, statusNotObserved),
				signal.Sleep:         want(statusNotMentioned),
				signal.Fatigue:       maybe(statusNotMentioned, statusNotObserved),
				signal.Appetite:      maybe(statusNotObserved, statusNotMentioned),
				signal.SelfBlame:     want(statusNotMentioned),
				signal.Concentration: maybe(statusNotMentioned, statusNotObserved),
				signal.Psychomotor:   want(statusNotMentioned),
			},
		}},
	},
	{
		name: "비꼬아 말한 하루",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("어젯밤엔 좀 쉬셨어요?"),
				user("아주 푹 쉬었지. 새벽 다섯 시까지 핸드폰만 보다가 한 시간 자고 나왔어"),
				assistant("거의 못 쉬셨네요."),
				user("덕분에 오늘 컨디션 최고야. 서류를 세 번 다시 읽어도 무슨 말인지 모르겠더라"),
				assistant("머리가 잘 안 돌아가셨겠어요."),
				user("밥도 아주 잘 먹었지. 아침 점심 다 굶고 저녁에 라면 하나 끓였으니까"),
			},
			gold: map[signal.Item]goldItem{
				signal.Interest: want(statusNotMentioned),
				signal.Mood:     want(statusNotMentioned),
				// 근거가 "아주 푹 쉬었지"에서 끊기면 화면에는 판단과 반대로 읽히는 문장이 걸린다.
				signal.Sleep:         needs(want(statusObserved), "새벽 다섯 시", "한 시간 자고"),
				signal.Fatigue:       maybe(statusNotMentioned, statusObserved),
				signal.Appetite:      needs(want(statusObserved), "굶고", "라면 하나"),
				signal.SelfBlame:     want(statusNotMentioned),
				signal.Concentration: needs(want(statusObserved), "세 번 다시 읽어도", "무슨 말인지 모르겠"),
				signal.Psychomotor:   want(statusNotMentioned),
			},
		}},
	},
	{
		name: "항목마다 아니라고 말한 하루",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("요즘 잠은 어떠세요?"),
				user("잠 문제는 없어. 눕자마자 자"),
				assistant("다행이네요. 입맛은 어떠세요?"),
				user("입맛 없는 것도 아니야. 오늘도 세 끼 다 먹었어"),
				assistant("일에 집중은 잘 되세요?"),
				user("집중 안 되는 건 아니야. 할 일은 다 끝냈어"),
				assistant("스스로를 탓하는 마음이 들 때는 없으세요?"),
				user("내 탓이라고 생각하진 않아"),
				assistant("몸은 무겁지 않으세요?"),
				user("피곤하지도 않아"),
			},
			// 부인은 관찰되지 않음이다. 언급 없음으로 새면 그날 무엇을 들었는지가 화면에서 사라진다.
			gold: map[signal.Item]goldItem{
				signal.Interest:      want(statusNotMentioned),
				signal.Mood:          want(statusNotMentioned),
				signal.Sleep:         want(statusNotObserved),
				signal.Fatigue:       want(statusNotObserved),
				signal.Appetite:      want(statusNotObserved),
				signal.SelfBlame:     want(statusNotObserved),
				signal.Concentration: want(statusNotObserved),
				signal.Psychomotor:   want(statusNotMentioned),
			},
		}},
	},
	{
		name: "지나가는 말로만 나온 신호",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("오늘은 뭐 하면서 보내셨어요?"),
				user("오전에 마트 갔다가 오후에는 조카 생일 선물을 골랐어"),
				assistant("바쁘셨네요. 선물은 뭐로 정하셨어요?"),
				user("레고로 했어. 고르는 데 오래 걸렸는데 조카가 좋아할 것 같아"),
				assistant("마음이 담긴 선물이네요."),
				user("아 근데 요 며칠 계속 잠을 설쳐서 마트에서는 좀 헤맸어"),
				assistant("피곤한 채로 다니셨네요."),
				user("그래도 선물 사고 나니까 기분은 괜찮았어"),
			},
			gold: map[signal.Item]goldItem{
				signal.Interest:      want(statusNotMentioned),
				signal.Mood:          want(statusNotObserved),
				signal.Sleep:         want(statusObserved),
				signal.Fatigue:       maybe(statusNotMentioned, statusObserved),
				signal.Appetite:      want(statusNotMentioned),
				signal.SelfBlame:     want(statusNotMentioned),
				signal.Concentration: maybe(statusNotMentioned, statusObserved),
				signal.Psychomotor:   want(statusNotMentioned),
			},
		}},
	},
	{
		name: "남의 어려움만 이야기한 하루",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("오늘은 어떤 하루였어요?"),
				user("친구 하나가 회사를 그만뒀는데 요즘 집에서 아무것도 안 하고 누워만 있다고 해"),
				assistant("친구가 걱정되시겠어요."),
				user("뭘 해도 재미가 없다고 하고 밥도 잘 안 먹는대. 전화 목소리에 힘이 하나도 없었어"),
				assistant("듣는 마음도 무거우셨겠어요."),
				user("그래서 주말에 만나기로 했어. 나는 오늘 평소랑 똑같았어"),
				assistant("친구에게 좋은 하루가 되겠네요."),
				user("그러면 좋겠어. 일 끝나고 바로 집에 와서 씻고 쉬었어"),
			},
			gold: map[signal.Item]goldItem{
				signal.Interest:      third(want(statusNotMentioned)),
				signal.Mood:          want(statusNotMentioned),
				signal.Sleep:         want(statusNotMentioned),
				signal.Fatigue:       third(want(statusNotMentioned)),
				signal.Appetite:      third(want(statusNotMentioned)),
				signal.SelfBlame:     want(statusNotMentioned),
				signal.Concentration: want(statusNotMentioned),
				signal.Psychomotor:   third(want(statusNotMentioned)),
			},
			foreign: []string{
				"아무것도 안 하고 누워만 있다고 해",
				"뭘 해도 재미가 없다고 하고",
				"밥도 잘 안 먹는대",
				"힘이 하나도 없었어",
			},
		}},
	},
	{
		name: "하루에 두 번 대화한 날",
		parts: []accuracyPart{
			{
				label: "아침",
				turns: []turn{
					assistant("좋은 아침이에요. 오늘 아침은 어떻게 시작했어요?"),
					user("어제 일찍 자서 아침에는 개운했어. 밥도 먹고 나왔어"),
					assistant("가볍게 시작하셨네요."),
					user("오늘 할 일이 많은데 기분은 나쁘지 않아"),
				},
				gold: map[signal.Item]goldItem{
					signal.Interest:      want(statusNotMentioned),
					signal.Mood:          want(statusNotObserved),
					signal.Sleep:         want(statusNotObserved),
					signal.Fatigue:       maybe(statusNotObserved, statusNotMentioned),
					signal.Appetite:      want(statusNotObserved),
					signal.SelfBlame:     want(statusNotMentioned),
					signal.Concentration: want(statusNotMentioned),
					signal.Psychomotor:   want(statusNotMentioned),
				},
			},
			{
				label: "저녁",
				turns: []turn{
					assistant("저녁이네요. 남은 하루는 어땠어요?"),
					user("오후에 완전히 방전됐어. 세 시쯤부터는 앉아만 있었어"),
					assistant("많이 지치셨네요."),
					user("저녁도 넘겼어. 먹을 기운도 없더라"),
					assistant("오늘은 일찍 쉬는 게 좋겠어요."),
					user("그럴게. 아침에는 괜찮았는데 왜 이러지"),
				},
				gold: map[signal.Item]goldItem{
					signal.Interest:      want(statusNotMentioned),
					signal.Mood:          want(statusNotMentioned),
					signal.Sleep:         want(statusNotMentioned),
					signal.Fatigue:       want(statusObserved),
					signal.Appetite:      want(statusObserved),
					signal.SelfBlame:     want(statusNotMentioned),
					signal.Concentration: maybe(statusNotMentioned, statusObserved),
					signal.Psychomotor:   maybe(statusNotMentioned, statusObserved),
				},
			},
		},
	},
	{
		name: "아주 짧은 대화",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("오늘 하루는 어땠어요?"),
				user("오늘은 그냥 너무 피곤해서 일찍 누울게"),
				assistant("푹 쉬세요. 내일 또 이야기해요."),
				user("응"),
			},
			gold: map[signal.Item]goldItem{
				signal.Interest:      want(statusNotMentioned),
				signal.Mood:          want(statusNotMentioned),
				signal.Sleep:         want(statusNotMentioned),
				signal.Fatigue:       want(statusObserved),
				signal.Appetite:      want(statusNotMentioned),
				signal.SelfBlame:     want(statusNotMentioned),
				signal.Concentration: want(statusNotMentioned),
				signal.Psychomotor:   want(statusNotMentioned),
			},
		}},
	},
	{
		name: "상대가 사용자가 하지 않은 말을 한 대화",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("오늘 하루는 어땠어요?"),
				user("오전에 도서관에서 책 두 권 빌렸어"),
				assistant("요즘 잠도 잘 못 자고 입맛도 없다고 하셨는데, 오늘은 좀 나아졌어요?"),
				user("도서관 끝나고는 친구랑 칼국수 먹었어"),
				assistant("계속 기운이 없다고 하셔서 걱정했어요. 저녁에는 뭐 하셨어요?"),
				user("집에 와서 빨래 돌리고 일찍 누웠어"),
			},
			// 잠, 입맛, 기운은 상대만 말했다. 사용자는 한마디도 하지 않았다.
			gold: map[signal.Item]goldItem{
				signal.Interest:      want(statusNotMentioned),
				signal.Mood:          want(statusNotMentioned),
				signal.Sleep:         want(statusNotMentioned),
				signal.Fatigue:       want(statusNotMentioned),
				signal.Appetite:      maybe(statusNotMentioned, statusNotObserved),
				signal.SelfBlame:     want(statusNotMentioned),
				signal.Concentration: want(statusNotMentioned),
				signal.Psychomotor:   want(statusNotMentioned),
			},
			foreign: []string{"잠도 잘 못 자고 입맛도 없다", "계속 기운이 없다"},
		}},
	},
	{
		name: "많이 자고 많이 먹은 하루",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("오늘 하루는 어땠어요?"),
				user("어제 열두 시간을 자고도 낮에 또 두 시간 잤어"),
				assistant("많이 주무셨네요."),
				user("밤에는 과자를 세 봉지나 뜯었어. 배가 부른데도 계속 먹었어"),
				assistant("마음이 헛헛하셨나 봐요."),
				user("다 먹고 나서 내가 왜 이러나 싶었어"),
			},
			// 두 항목은 반대 방향으로도 신호다. 너무 많이 잔 것과 지나치게 먹은 것을 놓치면 절반을 못 본다.
			gold: map[signal.Item]goldItem{
				signal.Interest:      want(statusNotMentioned),
				signal.Mood:          want(statusNotMentioned),
				signal.Sleep:         want(statusObserved),
				signal.Fatigue:       maybe(statusNotMentioned, statusObserved),
				signal.Appetite:      want(statusObserved),
				signal.SelfBlame:     want(statusObserved),
				signal.Concentration: want(statusNotMentioned),
				signal.Psychomotor:   want(statusNotMentioned),
			},
		}},
	},
	{
		name: "안절부절못한 하루",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("오늘 하루는 어땠어요?"),
				user("하루 종일 가만히 앉아 있지를 못했어. 다리를 계속 떨고 사무실을 몇 번이나 왔다 갔다 했어"),
				assistant("많이 초조하셨네요."),
				user("별일도 아닌데 자꾸 조급해져서 앉았다 일어났다만 했어"),
				assistant("잠은 어떠셨어요?"),
				user("잠은 그럭저럭 잤어"),
			},
			gold: map[signal.Item]goldItem{
				signal.Interest:      want(statusNotMentioned),
				signal.Mood:          maybe(statusNotMentioned, statusObserved),
				signal.Sleep:         want(statusNotObserved),
				signal.Fatigue:       want(statusNotMentioned),
				signal.Appetite:      want(statusNotMentioned),
				signal.SelfBlame:     want(statusNotMentioned),
				signal.Concentration: want(statusNotMentioned),
				signal.Psychomotor:   want(statusObserved),
			},
		}},
	},
	{
		name: "시연만큼 긴 대화",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("오늘 하루는 어땠어요?"),
				user("음 오늘은 좀 정신없었어. 아침부터 일이 몰려서"),
				assistant("바쁜 하루였네요. 아침엔 어떻게 시작했어요?"),
				user("여섯 시 반에 알람 맞춰 놨는데 다섯 시에 눈이 떠져서 그냥 일어났어"),
				assistant("일찍 깨셨네요."),
				user("요 며칠 계속 그래. 네다섯 시쯤 되면 저절로 눈이 떠져"),
				assistant("며칠 이어지면 힘드시죠. 출근길은 어땠어요?"),
				user("지하철에서 서서 왔어. 사람이 너무 많아서"),
				assistant("고단한 출근이었네요."),
				user("회사 와서는 오전에 회의가 두 개 있었고"),
				assistant("회의는 어땠어요?"),
				user("두 번째 회의는 내가 발표였는데 자료를 다시 확인할 시간이 없었어"),
				assistant("마음이 급하셨겠어요."),
				user("그래도 어떻게 넘어갔어. 팀장님이 괜찮다고 해 주셨고"),
				assistant("다행이네요. 점심은 드셨어요?"),
				user("점심은 회사 앞 김밥집에서 라면 먹었어"),
				assistant("간단히 드셨네요. 오후에는요?"),
				user("오후에는 메일 정리하고 다음 주 일정 잡았어"),
				assistant("차분한 오후였네요."),
				user("아 근데 오후 세 시쯤부터 진짜 눈이 감기더라. 커피 두 잔 마셨는데도 안 되겠어서"),
				assistant("많이 졸리셨네요."),
				user("결국 회의실 가서 십 분 엎드려 있었어"),
				assistant("조금이라도 쉬셨네요."),
				user("퇴근하고는 마트 들러서 우유랑 계란 샀어"),
				assistant("장도 보셨네요. 저녁은 어떻게 하셨어요?"),
				user("집에 와서 계란찜 하고 밥 먹었어. 배는 잘 들어가"),
				assistant("잘 챙겨 드셨네요."),
				user("먹고 나서 설거지하고 빨래 개고"),
				assistant("부지런한 저녁이네요."),
				user("드라마 한 편 보려다가 그냥 껐어. 예전엔 그거 기다렸는데 요즘은 별로 보고 싶지가 않아"),
				assistant("좋아하던 것이 시들해지면 서운하죠."),
				user("그런가. 그냥 좀 귀찮은 거 같기도 하고"),
				assistant("오늘 이야기해 주셔서 고마워요. 내일도 이야기해요."),
				user("응 그럴게"),
			},
			// 시연에서 실제로 오갈 만한 길이다. 신호는 잡담 사이에 흩어져 있다.
			gold: map[signal.Item]goldItem{
				signal.Interest:      want(statusObserved),
				signal.Mood:          want(statusNotMentioned),
				signal.Sleep:         want(statusObserved),
				signal.Fatigue:       want(statusObserved),
				signal.Appetite:      want(statusNotObserved),
				signal.SelfBlame:     want(statusNotMentioned),
				signal.Concentration: want(statusNotMentioned),
				signal.Psychomotor:   want(statusNotMentioned),
			},
		}},
	},
	{
		name: "지난 일을 떠올린 하루",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("오늘 하루는 어땠어요?"),
				user("오늘은 별일 없었어. 그냥 회사 갔다 왔어"),
				assistant("편안한 하루였네요."),
				user("근데 아까 옛날 사진 보다가 재수하던 때 생각이 났어"),
				assistant("그때는 어떠셨어요?"),
				user("그때는 진짜 매일 두세 시간밖에 못 자고 입맛도 하나도 없었어"),
				assistant("많이 힘든 시간이었네요."),
				user("지금은 그때보다 훨씬 나아. 오늘은 세 끼 다 잘 챙겨 먹었고"),
			},
			// 한참 전 일은 요즘의 신호가 아니다. 지난 일이 오늘로 넘어오면 점수가 부풀려진다.
			gold: map[signal.Item]goldItem{
				signal.Interest:      want(statusNotMentioned),
				signal.Mood:          maybe(statusNotMentioned, statusNotObserved),
				signal.Sleep:         want(statusNotMentioned),
				signal.Fatigue:       want(statusNotMentioned),
				signal.Appetite:      want(statusNotObserved),
				signal.SelfBlame:     want(statusNotMentioned),
				signal.Concentration: want(statusNotMentioned),
				signal.Psychomotor:   want(statusNotMentioned),
			},
		}},
	},
	{
		name: "하루 안에서 뒤집히는 하루",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("어젯밤 잠은 어땠어요?"),
				user("어젯밤엔 세 번쯤 깼어. 자다 깨다 했어"),
				assistant("푹 못 쉬셨네요."),
				user("그래도 아침에 한 시간 더 누워 있다가 일어났더니 좀 낫더라"),
				assistant("낮에는 어땠어요?"),
				user("점심때까지는 입맛이 하나도 없었는데 저녁엔 삼겹살 두 인분 먹었어"),
				assistant("저녁은 잘 드셨네요."),
				user("응 저녁엔 괜찮았어"),
			},
			// 하루 안에서 나아졌다고 신호가 없던 것이 되지는 않는다.
			//
			// 입맛은 한 발화 안에서 뜻이 뒤집힌다. 앞토막("점심때까지는 입맛이 하나도 없었는데")만 근거로 오면
			// 판단 자체는 방어할 수 있어도, 근거만 따로 보여주는 화면에서는 뒤 문장을 일부러 잘라 낸 인용으로 읽힌다.
			// 그래서 어느 쪽으로 판단하든 뒤집힌 뒤가 근거에 들어 있어야 한다.
			gold: map[signal.Item]goldItem{
				signal.Interest:      want(statusNotMentioned),
				signal.Mood:          want(statusNotMentioned),
				signal.Sleep:         want(statusObserved),
				signal.Fatigue:       maybe(statusNotMentioned, statusNotObserved),
				signal.Appetite:      needs(maybe(statusObserved, statusNotObserved), "삼겹살"),
				signal.SelfBlame:     want(statusNotMentioned),
				signal.Concentration: want(statusNotMentioned),
				signal.Psychomotor:   want(statusNotMentioned),
			},
		}},
	},
	{
		name: "받아쓴 말이 거친 대화",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("오늘 하루는 어땠어요?"),
				user("어 그니까 어제 밤에 잠을 잠을 잘 못 잤어 음 새벽에 한 세 번쯤 깬 것 같애"),
				assistant("자꾸 깨셨네요."),
				user("그래서 오늘 뭐 하루 종일 좀 멍하고 기운이 없었어 진짜"),
				assistant("많이 지치셨겠어요."),
				user("밥은 뭐 그냥 먹었고 어 점심은 김치찌개 먹었나 그랬어"),
				assistant("오늘 이야기 들려줘서 고마워요."),
				user("어 그래"),
			},
			// 음성을 받아쓴 글에는 군말과 잘못 적힌 낱말이 섞인다. 근거를 다듬어 옮기면 대조에서 버려지고 그 항목은 사라진다.
			gold: map[signal.Item]goldItem{
				signal.Interest:      want(statusNotMentioned),
				signal.Mood:          want(statusNotMentioned),
				signal.Sleep:         want(statusObserved),
				signal.Fatigue:       want(statusObserved),
				signal.Appetite:      maybe(statusNotObserved, statusNotMentioned),
				signal.SelfBlame:     want(statusNotMentioned),
				signal.Concentration: maybe(statusNotMentioned, statusObserved),
				signal.Psychomotor:   want(statusNotMentioned),
			},
		}},
	},
	// 아래 세 건은 양성 표본이 얇았던 항목(psychomotor, interest, mood, self_blame)을 채우려고 더한 대화다.
	// 한 실행에 관찰됨 이름표가 한두 자리뿐이면 그 항목의 재현율은 자리 몇 개에서 나온 값이 되어 아무것도 말해 주지 못한다.
	{
		name: "움직임이 느려진 하루",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("오늘 하루는 어떻게 보내셨어요?"),
				user("오늘은 뭘 해도 몸이 느렸어. 옷 갈아입는 데도 한참 걸렸어"),
				assistant("천천히 흐르는 하루였네요."),
				user("말할 때도 한 마디 하고 한참 뜸을 들였어. 동작이 다 느려진 것 같아"),
				assistant("기분은 어떠셨어요?"),
				user("계속 가라앉아 있었어. 하루 내내 마음이 무거웠어"),
				assistant("잠은 어떠셨어요?"),
				user("잠은 잘 잤어"),
			},
			gold: map[signal.Item]goldItem{
				signal.Interest: want(statusNotMentioned),
				signal.Mood:     want(statusObserved),
				signal.Sleep:    want(statusNotObserved),
				// "몸이 느렸어"를 기운 없음으로 읽을 수도 있어 갈리는 자리로 둔다.
				signal.Fatigue:       maybe(statusNotMentioned, statusObserved),
				signal.Appetite:      want(statusNotMentioned),
				signal.SelfBlame:     want(statusNotMentioned),
				signal.Concentration: want(statusNotMentioned),
				signal.Psychomotor:   want(statusObserved),
			},
		}},
	},
	{
		name: "좋아하던 것이 시들해진 하루",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("오늘 하루는 어땠어요?"),
				user("기타를 삼 년 쳤는데 요즘은 꺼내 볼 생각도 안 들어. 하나도 재미가 없어"),
				assistant("좋아하던 게 시들해졌네요."),
				user("기분도 계속 처져 있어. 아무 일 없는데 괜히 눈물이 나더라"),
				assistant("많이 힘드셨겠어요."),
				user("다 내가 못나서 이렇게 된 것 같아. 쓸모없는 사람 같아"),
				assistant("오늘 이야기해 줘서 고마워요."),
				user("응 고마워"),
			},
			gold: map[signal.Item]goldItem{
				signal.Interest:      want(statusObserved),
				signal.Mood:          want(statusObserved),
				signal.Sleep:         want(statusNotMentioned),
				signal.Fatigue:       want(statusNotMentioned),
				signal.Appetite:      want(statusNotMentioned),
				signal.SelfBlame:     want(statusObserved),
				signal.Concentration: want(statusNotMentioned),
				signal.Psychomotor:   want(statusNotMentioned),
			},
		}},
	},
	{
		name: "초조해서 아무것도 붙잡지 못한 하루",
		parts: []accuracyPart{{
			turns: []turn{
				assistant("오늘 하루는 어땠어요?"),
				user("아침부터 마음이 급해서 한자리에 앉아 있지 못했어. 손톱만 계속 물어뜯었어"),
				assistant("마음이 붙잡히지 않는 하루였네요."),
				user("주말마다 가던 모임도 이제 나가고 싶지 않아. 다 귀찮아졌어"),
				assistant("좋아하던 것도 시들하네요."),
				user("그런 내가 한심해서 계속 스스로를 나무랐어"),
				assistant("스스로에게 엄격하시네요. 잠은 어떠셨어요?"),
				user("어젯밤엔 여섯 시간쯤 잤어"),
			},
			gold: map[signal.Item]goldItem{
				signal.Interest: want(statusObserved),
				// "한심해서"를 가라앉은 기분으로 읽을 수도 있다.
				signal.Mood:      maybe(statusNotMentioned, statusObserved),
				signal.Sleep:     maybe(statusNotObserved, statusNotMentioned),
				signal.Fatigue:   want(statusNotMentioned),
				signal.Appetite:  want(statusNotMentioned),
				signal.SelfBlame: want(statusObserved),
				// "한자리에 앉아 있지 못했어"를 집중 곤란으로 읽을 수도 있다.
				signal.Concentration: maybe(statusNotMentioned, statusObserved),
				signal.Psychomotor:   want(statusObserved),
			},
		}},
	},
}

// judgementRow는 채점한 판단 하나다. 어느 대화의 몇 번째 실행에서 어떤 항목이 어떻게 나왔는지를 담는다.
type judgementRow struct {
	conversation string
	run          int
	item         signal.Item
	gold         goldItem
	got          string
	explicitness string
	evidence     string
	hasEvidence  bool
}

// runStats는 한 번의 추출이 어떻게 끝났는지다.
type runStats struct {
	conversation string
	run          int
	elapsed      time.Duration
	attempts     int
	quotes       int
	notVerbatim  int
	dropped      int
	dropReasons  map[string]int
	repaired     int
	lineMismatch int
	unanchored   int
	model        string
}

// accuracyReport는 모든 대화의 결과를 모아 숫자를 낸다.
type accuracyReport struct {
	// label은 무엇으로 뽑은 결과인지다.
	label string
	// strict가 참이면 목표에 못 미친 값에서 시험이 깨진다. 견줄 값을 낼 때는 숫자만 남긴다.
	strict bool
	mu     sync.Mutex
	rows   []judgementRow
	runs   []runStats
	fails  []string
	// foreign은 근거가 남의 말이었던 자리다. 화면에 사용자의 근거로 걸리면 안 되는 글이다.
	foreign []string
	// leaks는 남의 증상이 사용자의 관찰됨으로 넘어온 자리다.
	leaks []string
	// misleading은 근거가 뜻이 뒤집히기 전의 앞토막만 담은 자리다.
	misleading []string
}

func (r *accuracyReport) addRow(row judgementRow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, row)
}

func (r *accuracyReport) addRun(stats runStats) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs = append(r.runs, stats)
}

func (r *accuracyReport) addFail(where string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fails = append(r.fails, where)
}

func (r *accuracyReport) addForeign(where string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.foreign = append(r.foreign, where)
}

func (r *accuracyReport) addLeak(where string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.leaks = append(r.leaks, where)
}

func (r *accuracyReport) addMisleading(where string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.misleading = append(r.misleading, where)
}

// graded는 채점한 한 자리의 결과다.
type graded int

const (
	gradeTruePositive graded = iota
	gradeFalsePositive
	gradeFalseNegative
	gradeTrueNegative
	// gradeExcluded는 사람이 읽어도 갈리는 자리라 채점에서 뺐다는 뜻이다.
	gradeExcluded
)

// grade는 관찰됨을 찾아내는 일로 보고 한 자리를 채점한다.
// 관찰됨만 점수의 분자에 들어가므로, 화면의 숫자를 흔드는 것은 관찰됨의 거짓 양성과 놓침이다.
func grade(row judgementRow) graded {
	if row.got != row.gold.status {
		for _, allowed := range row.gold.allow {
			if row.got == allowed {
				return gradeExcluded
			}
		}
	}
	switch {
	case row.gold.status == statusObserved && row.got == statusObserved:
		return gradeTruePositive
	case row.got == statusObserved:
		return gradeFalsePositive
	case row.gold.status == statusObserved:
		return gradeFalseNegative
	default:
		return gradeTrueNegative
	}
}

// counter는 항목 하나의 채점 결과를 센다.
type counter struct {
	tp, fp, fn, tn, excluded int
	// exact는 이름표와 그대로 맞은 자리의 수다(갈리는 자리는 받아들인 값도 맞은 것으로 센다).
	exact, total int
}

func (c counter) precision() (float64, bool) {
	if c.tp+c.fp == 0 {
		return 0, false
	}
	return float64(c.tp) / float64(c.tp+c.fp), true
}

func (c counter) recall() (float64, bool) {
	if c.tp+c.fn == 0 {
		return 0, false
	}
	return float64(c.tp) / float64(c.tp+c.fn), true
}

func (c *counter) add(row judgementRow) {
	c.total++
	if row.got == row.gold.status {
		c.exact++
	}
	switch grade(row) {
	case gradeTruePositive:
		c.tp++
	case gradeFalsePositive:
		c.fp++
	case gradeFalseNegative:
		c.fn++
	case gradeTrueNegative:
		c.tn++
	case gradeExcluded:
		c.excluded++
		c.exact++
	}
}

func rate(part, whole int) string {
	if whole == 0 {
		return "-"
	}
	return fmt.Sprintf("%.3f (%d/%d)", float64(part)/float64(whole), part, whole)
}

func ratio(num, den int) (float64, bool) {
	if den == 0 {
		return 0, false
	}
	return float64(num) / float64(den), true
}

// print는 모은 결과를 사람이 읽을 표로 낸다.
func (r *accuracyReport) print(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.rows) == 0 {
		return
	}

	perItem := map[signal.Item]*counter{}
	overall := &counter{}
	for _, row := range r.rows {
		c, ok := perItem[row.item]
		if !ok {
			c = &counter{}
			perItem[row.item] = c
		}
		c.add(row)
		overall.add(row)
	}

	var b strings.Builder
	b.WriteString("\n[" + r.label + "] 항목별 (관찰됨을 찾아내는 일로 본 정밀도와 재현율)\n")
	fmt.Fprintf(&b, "%-14s %5s %5s %5s %5s %5s  %-18s %-18s %s\n",
		"항목", "TP", "FP", "FN", "TN", "제외", "정밀도", "재현율", "세 갈래 일치")
	for _, item := range signal.AllItems() {
		c := perItem[item]
		if c == nil {
			continue
		}
		fmt.Fprintf(&b, "%-14s %5d %5d %5d %5d %5d  %-18s %-18s %s\n",
			item.String(), c.tp, c.fp, c.fn, c.tn, c.excluded,
			rate(c.tp, c.tp+c.fp), rate(c.tp, c.tp+c.fn), rate(c.exact, c.total))
	}
	fmt.Fprintf(&b, "%-14s %5d %5d %5d %5d %5d  %-18s %-18s %s\n",
		"전체", overall.tp, overall.fp, overall.fn, overall.tn, overall.excluded,
		rate(overall.tp, overall.tp+overall.fp), rate(overall.tp, overall.tp+overall.fn),
		rate(overall.exact, overall.total))

	// 대화마다 몇 자리가 어긋났는지.
	perConversation := map[string]*counter{}
	order := []string{}
	for _, row := range r.rows {
		c, ok := perConversation[row.conversation]
		if !ok {
			c = &counter{}
			perConversation[row.conversation] = c
			order = append(order, row.conversation)
		}
		c.add(row)
	}
	sort.Strings(order)
	b.WriteString("\n대화별 (세 번의 실행을 합친 값)\n")
	for _, name := range order {
		c := perConversation[name]
		fmt.Fprintf(&b, "  %-34s 일치 %-14s FP %d FN %d 제외 %d\n",
			name, rate(c.exact, c.total), c.fp, c.fn, c.excluded)
	}

	// 같은 대화를 다시 돌렸을 때의 흔들림.
	statuses := map[string]map[string]int{}
	quotes := map[string]map[string]int{}
	for _, row := range r.rows {
		key := row.conversation + "/" + row.item.String()
		if statuses[key] == nil {
			statuses[key] = map[string]int{}
			quotes[key] = map[string]int{}
		}
		statuses[key][row.got]++
		if row.hasEvidence {
			quotes[key][row.evidence]++
		}
	}
	unstableStatus, unstableQuote, quoteSlots := 0, 0, 0
	for key, seen := range statuses {
		if len(seen) > 1 {
			unstableStatus++
		}
		if len(quotes[key]) > 0 {
			quoteSlots++
			if len(quotes[key]) > 1 {
				unstableQuote++
			}
		}
	}

	// 저장된 근거가 화면에서 어떻게 읽히는지. 판단이 맞는 것과는 따로 센다.
	storedQuotes, dangling := 0, 0
	danglingSeen := map[string]int{}
	for _, row := range r.rows {
		if !row.hasEvidence {
			continue
		}
		storedQuotes++
		if endsDangling(row.evidence) {
			dangling++
			danglingSeen[row.item.String()+": "+row.evidence]++
		}
	}

	// 근거와 시간.
	var (
		totalQuotes, totalNotVerbatim, totalDropped, totalRepaired, totalMismatch, totalUnanchored, retried int
		reasons                                                                                             = map[string]int{}
		elapsed                                                                                             []time.Duration
		models                                                                                              = map[string]int{}
	)
	for _, run := range r.runs {
		totalQuotes += run.quotes
		totalNotVerbatim += run.notVerbatim
		totalDropped += run.dropped
		totalRepaired += run.repaired
		totalMismatch += run.lineMismatch
		totalUnanchored += run.unanchored
		if run.attempts > 1 {
			retried++
		}
		for reason, n := range run.dropReasons {
			reasons[reason] += n
		}
		elapsed = append(elapsed, run.elapsed)
		models[run.model]++
	}
	sort.Slice(elapsed, func(i, j int) bool { return elapsed[i] < elapsed[j] })
	pick := func(q float64) time.Duration {
		if len(elapsed) == 0 {
			return 0
		}
		idx := int(q * float64(len(elapsed)-1))
		return elapsed[idx].Round(time.Millisecond)
	}

	fmt.Fprintf(&b, "\n근거: 모델이 내놓은 토막 %d개, 그 가운데 글자 그대로 %s\n",
		totalQuotes, rate(totalQuotes-totalNotVerbatim, totalQuotes))
	fmt.Fprintf(&b, "버린 항목 %d개 %v, 명시성 고친 항목 %d개, 줄 번호 어긋남 %d개, 발화를 가리키지 못한 근거 %d개\n",
		totalDropped, reasons, totalRepaired, totalMismatch, totalUnanchored)
	fmt.Fprintf(&b, "저장된 근거 %d개, 그 가운데 이음말로 끝나 말이 끊긴 토막 %s\n",
		storedQuotes, rate(dangling, storedQuotes))
	if len(danglingSeen) > 0 && os.Getenv("SIGNAL_ACCURACY_PRINT") == "1" {
		// 남은 자리가 사용자의 말 자체가 그렇게 끝난 것인지, 모델이 더 자를 수 있는데 자른 것인지 눈으로 가린다.
		seen := make([]string, 0, len(danglingSeen))
		for quote, n := range danglingSeen {
			seen = append(seen, fmt.Sprintf("%dx %s", n, quote))
		}
		sort.Strings(seen)
		b.WriteString("  끊긴 토막:\n    " + strings.Join(seen, "\n    ") + "\n")
	}
	fmt.Fprintf(&b, "다시 돌렸을 때: 판단이 갈린 자리 %s, 근거 토막이 갈린 자리 %s\n",
		rate(unstableStatus, len(statuses)), rate(unstableQuote, quoteSlots))
	fmt.Fprintf(&b, "걸린 시간: 가장 짧게 %v, 중간 %v, 90%% %v, 가장 길게 %v (추출 %d번, 다시 부른 실행 %d번)\n",
		pick(0), pick(0.5), pick(0.9), pick(1), len(elapsed), retried)
	fmt.Fprintf(&b, "모델: %v\n", models)

	if len(r.foreign) > 0 {
		sort.Strings(r.foreign)
		b.WriteString("\n[치명] 근거가 사용자의 말이 아니었다:\n  " + strings.Join(r.foreign, "\n  ") + "\n")
	}
	if len(r.leaks) > 0 {
		sort.Strings(r.leaks)
		b.WriteString("\n[높음] 남의 증상이 사용자의 관찰됨이 되었다:\n  " + strings.Join(r.leaks, "\n  ") + "\n")
	}
	if len(r.misleading) > 0 {
		sort.Strings(r.misleading)
		b.WriteString("\n[높음] 근거가 뜻이 뒤집히기 전의 앞토막이었다:\n  " + strings.Join(r.misleading, "\n  ") + "\n")
	}
	if len(r.fails) > 0 {
		sort.Strings(r.fails)
		b.WriteString("\n추출이 끝내 실패한 실행:\n  " + strings.Join(r.fails, "\n  ") + "\n")
	}
	t.Log(b.String())

	if !r.strict {
		return
	}
	precision, okP := overall.precision()
	recall, okR := overall.recall()
	if okP {
		assert.GreaterOrEqual(t, precision, targetPrecision, "관찰됨 판단의 정밀도가 목표에 못 미친다")
	}
	if okR {
		assert.GreaterOrEqual(t, recall, targetRecall, "관찰됨 판단의 재현율이 목표에 못 미친다")
	}
	if verbatim, ok := ratio(totalQuotes-totalNotVerbatim, totalQuotes); ok {
		assert.GreaterOrEqual(t, verbatim, minVerbatimRate, "모델이 내놓은 근거가 너무 자주 기록에 없는 글이다")
	}
	if danglingRate, ok := ratio(dangling, storedQuotes); ok {
		assert.LessOrEqual(t, danglingRate, maxDanglingQuoteRate,
			"근거가 이음말로 끝나 말이 끊긴 조각으로 화면에 걸린다")
	}
	assert.Empty(t, r.foreign, "사용자가 하지 않은 말이 근거로 저장되었다")
	assert.Empty(t, r.leaks, "남의 증상이 사용자의 신호로 넘어왔다")
	assert.Empty(t, r.misleading, "근거가 뜻이 뒤집히기 전의 앞토막만 담고 있다")
	assert.Empty(t, r.fails, "추출이 끝내 실패한 실행이 있다")
}

func TestLiveExtractAccuracy(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIGNAL_ACCURACY_TESTS") != "1" {
		t.Skip("모델을 예순여섯 번 부르는 계측 시험이다. SIGNAL_ACCURACY_TESTS=1일 때만 돈다")
	}

	report := &accuracyReport{label: "실제 분석 모델", strict: true}
	t.Cleanup(func() { report.print(t) })

	for _, tc := range accuracyCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			llm := liveLLM(t, f)
			service := f.newService(func(o *analysis.Options) {
				o.LLM = llm
				o.Thinking = liveThinking()
			})
			for run := 1; run <= accuracyRepeats; run++ {
				for _, part := range tc.parts {
					name := tc.name
					if part.label != "" {
						name += "/" + part.label
					}
					measurePart(t, f, service, report, name, part, run)
				}
			}
			f.assertLogsClean()
		})
	}
}

// measurePart는 대화 한 건을 한 번 뽑아 채점한다. 실행마다 새 대화를 열어야 한다(분석은 대화마다 한 번만 돈다).
func measurePart(t *testing.T, f *fixture, service *analysis.Service, report *accuracyReport,
	name string, part accuracyPart, run int,
) {
	t.Helper()
	for _, item := range signal.AllItems() {
		if _, ok := part.gold[item]; !ok {
			t.Fatalf("%s: 여덟 항목에 모두 이름표를 붙여야 한다 (빠진 항목: %s)", name, item)
		}
	}

	c := f.open(part.turns...)
	f.end(c)

	result, attempts, elapsed, err := extractMeasured(t, service, c.target(f.userID))
	where := fmt.Sprintf("%s (실행 %d)", name, run)
	if err != nil {
		t.Logf("%s: 추출 실패 %v", where, err)
		report.addFail(where)
		return
	}
	if result.Outcome != analysis.OutcomeSaved {
		t.Logf("%s: 저장까지 가지 않았다 (%s)", where, result.Outcome)
		report.addFail(where + " outcome=" + string(result.Outcome))
		return
	}

	rows := f.assertEightRows(c)
	quotes := result.Counts.Observed + result.Counts.NotObserved + result.Dropped
	report.addRun(runStats{
		conversation: name, run: run, elapsed: elapsed, attempts: attempts,
		quotes: quotes, notVerbatim: result.DropReasons[dropNotVerbatimName],
		dropped: result.Dropped, dropReasons: result.DropReasons,
		repaired: result.Repaired, lineMismatch: result.LineMismatch, unanchored: result.Unanchored,
		model: result.Model,
	})

	var printed strings.Builder
	for _, item := range signal.AllItems() {
		stored := rows[item.String()]
		gold := part.gold[item]
		row := judgementRow{
			conversation: name, run: run, item: item, gold: gold,
			got: stored.status, explicitness: stored.explicitness,
			evidence: stored.evidence, hasEvidence: stored.hasEvidence,
		}
		report.addRow(row)

		if row.hasEvidence {
			f.remember(row.evidence)
			for _, foreign := range part.foreign {
				if strings.Contains(row.evidence, foreign) {
					report.addForeign(where + " " + item.String() + ": 남의 말을 근거로 썼다")
				}
			}
			if len(gold.needInQuote) > 0 && !containsAny(row.evidence, gold.needInQuote) {
				report.addMisleading(where + " " + item.String() + ": 뜻이 뒤집히기 전의 앞토막만 근거로 썼다")
			}
		}
		if gold.thirdParty && row.got == statusObserved {
			report.addLeak(where + " " + item.String())
		}

		printed.WriteString("\n  " + item.String() + " = " + row.got + "/" + row.explicitness +
			" (이름표 " + gold.status + ")")
		if os.Getenv("SIGNAL_ACCURACY_PRINT") == "1" && row.hasEvidence {
			printed.WriteString("  근거: " + row.evidence)
		}
	}
	t.Logf("[%s] model=%s %v dropped=%d(%v) repaired=%d line_mismatch=%d%s",
		where, result.Model, elapsed.Round(time.Millisecond), result.Dropped, result.DropReasons,
		result.Repaired, result.LineMismatch, printed.String())
}

// dropNotVerbatimName은 근거가 기록에 글자 그대로 없어서 버렸다는 까닭의 이름이다. 패키지 안의 상수와 같은 값이다.
const dropNotVerbatimName = "not_verbatim"

func containsAny(text string, fragments []string) bool {
	for _, fragment := range fragments {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}

// extractMeasured는 큐가 하듯이 다시 해 볼 만한 실패면 다시 부르고, 마지막 시도가 걸린 시간을 함께 돌려준다.
func extractMeasured(t *testing.T, service *analysis.Service, target analysis.Target) (
	analysis.Result, int, time.Duration, error,
) {
	t.Helper()
	var (
		result  analysis.Result
		err     error
		elapsed time.Duration
	)
	for attempt := 1; attempt <= liveAttempts; attempt++ {
		started := clock.Real{}.Now()
		result, err = service.Extract(t.Context(), target)
		elapsed = clock.Real{}.Now().Sub(started)
		if err == nil || analysis.Permanent(err) {
			return result, attempt, elapsed, err
		}
		t.Logf("시도 %d 실패: %v", attempt, err)
	}
	return result, liveAttempts, elapsed, err
}

// TestScriptedExtractAccuracy는 같은 이름표로, 키 없이 뜬 서버가 쓰는 정해 둔 답 모델을 잰다.
//
// 시연 기계에 키가 없으면 서버는 조용히 이 모델로 뜬다. 그때 화면에 차는 신호가 얼마나 다른지를 숫자로 남긴다.
// 이 모델은 낱말 표일 뿐이라 목표를 재지 않는다. 견줄 값만 낸다.
func TestScriptedExtractAccuracy(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIGNAL_ACCURACY_TESTS") != "1" {
		t.Skip("SIGNAL_ACCURACY_TESTS=1일 때만 도는 계측 시험이다")
	}

	report := &accuracyReport{label: "정해 둔 답 모델(키 없이 뜬 서버)"}
	t.Cleanup(func() { report.print(t) })

	for _, tc := range accuracyCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			model, err := scripted.New(config.AIProviderScripted, scripted.Options{})
			require.NoError(t, err)
			service := f.newService(func(o *analysis.Options) { o.LLM = model })
			for _, part := range tc.parts {
				name := tc.name
				if part.label != "" {
					name += "/" + part.label
				}
				measurePart(t, f, service, report, name, part, 1)
			}
		})
	}
}
