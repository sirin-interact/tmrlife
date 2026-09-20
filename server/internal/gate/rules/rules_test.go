package rules_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/unicode/norm"

	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/gate/gatetest"
	"github.com/sirin-interact/tmrlife/server/internal/gate/rules"
)

func lexicon(t *testing.T) *rules.Lexicon {
	t.Helper()
	lex, err := rules.LoadEmbedded()
	require.NoError(t, err)
	return lex
}

// checkShape는 어떤 글에서든 지켜져야 하는 판정의 모양을 본다.
func checkShape(t *testing.T, text string, got rules.Result) {
	t.Helper()
	assert.Equal(t, got.Stage >= crisis.StageCheck, got.Matched, "걸린 말만 확인 단계 이상이고, 가려낸 말은 걸리지 않은 것으로 넘긴다")
	assert.Equal(t, got.Matched, len(got.EntryIDs) > 0)
	if got.Matched {
		assert.NotEmpty(t, got.Evidence)
		assert.Contains(t, text, got.Evidence, "근거는 원문에 글자 그대로 있어야 한다")
	} else {
		assert.Empty(t, got.Evidence)
	}
	assert.Equal(t, crisis.RuleResult{Stage: got.Stage, Matched: got.Matched}, got.Core())
}

func TestLabelledSentences(t *testing.T) {
	lex := lexicon(t)

	for _, c := range gatetest.Cases() {
		name := fmt.Sprintf("%s/%s", c.Group, c.Utterance)
		if !c.RuleDecides {
			name = fmt.Sprintf("%s/판별 모델만 가를 수 있다/%s", c.Group, c.Utterance)
		}
		t.Run(name, func(t *testing.T) {
			got := lex.Scan(c.Utterance)
			checkShape(t, c.Utterance, got)

			assert.LessOrEqual(t, int(got.Stage), c.Max,
				"규칙이 범위를 넘기면 판별 모델이 문맥을 읽어도 되돌릴 수 없다 (걸린 항목: %v)", got.EntryIDs)
			if c.RuleDecides {
				assert.GreaterOrEqual(t, int(got.Stage), c.Min, "규칙만으로 잡아야 하는 표현이다 (가린 패턴: %v)", got.ExcludedBy)
			} else {
				require.NotEmpty(t, c.Why, "규칙에 맡기지 않는 문장에는 까닭을 적는다")
			}
		})
	}
}

func TestExplicitExpressions(t *testing.T) {
	lex := lexicon(t)

	tests := []struct {
		text  string
		stage crisis.Stage
		entry string
	}{
		// 방법
		{"수면제를 조금씩 모으고 있어", 3, "s3.method.pills_hoard"},
		{"약 한꺼번에 먹으려고", 3, "s3.method.overdose"},
		{"수면제 다 먹어버릴까", 3, "s3.method.overdose"},
		{"어제 약을 한움큼 먹었어", 3, "s3.method.overdose"},
		{"번개탄 사놨어", 3, "s3.method.charcoal"},
		{"목 매달고 싶다", 3, "s3.method.hanging"},
		{"옥상에서 뛰어내리고 싶다", 3, "s3.method.jump"},
		{"그냥 뛰어내릴까 생각했어", 3, "s3.method.jump"},
		{"한강에 뛰어들까", 3, "s3.method.jump"},
		{"난간에 한참 서 있었어", 3, "s3.method.jump"},
		{"자살 방법 검색해봤어", 3, "s3.method.search"},
		{"안 아프게 죽는 법 찾아봤어", 3, "s3.method.search"},
		{"커터칼 옆에 두고 있어", 3, "s3.method.blade"},
		{"농약 구해놨어", 3, "s3.method.poison"},
		// 계획과 시점
		{"죽으려고 했는데 무서웠어", 3, "s3.plan.intent"},
		{"죽기로 마음먹었어", 3, "s3.plan.intent"},
		{"죽을 준비 다 했어", 3, "s3.plan.intent"},
		{"자살할 거야", 3, "s3.plan.intent"},
		{"죽을 날짜도 정했어", 3, "s3.plan.intent"},
		{"오늘 죽을 거야", 3, "s3.plan.timed"},
		{"내일 다 끝낼 거야", 3, "s3.plan.timed"},
		{"이번 주말에 끝내기로 했어", 3, "s3.plan.timed"},
		// 준비와 작별
		{"유언장 써뒀어", 3, "s3.prep.will"},
		{"요즘 신변정리 하고 있어", 3, "s3.prep.affairs"},
		{"그동안 정말 감사했습니다", 3, "s3.farewell.thanks"},
		{"이게 내 마지막 인사야", 3, "s3.farewell.last"},
		{"내가 없어도 다들 잘 지내", 3, "s3.farewell.bye"},
		{"엄마 미안해 먼저 갈게", 3, "s3.farewell.bye"},

		// 지금의 바람
		{"죽어버리고 싶다", 2, "s2.wish.die"},
		{"죽고만 싶어", 2, "s2.wish.die"},
		{"죽었으면 좋겠다", 2, "s2.wish.die"},
		{"콱 죽어버릴까", 2, "s2.wish.die"},
		{"차라리 죽는 게 낫겠다", 2, "s2.wish.die"},
		{"죽으면 편할까", 2, "s2.wish.die"},
		{"죽어야 끝나나 싶어", 2, "s2.wish.die"},
		{"차라리 죽을래", 2, "s2.wish.die"},
		{"죽고 싶지 않은 날이 없어", 2, "s2.wish.always"},
		{"디지고 싶다", 2, "s2.wish.die_slang"},
		{"자살하고 싶다", 2, "s2.wish.suicide"},
		{"자살 생각이 자꾸 나", 2, "s2.wish.suicide"},
		{"자살 충동이 와", 2, "s2.wish.suicide"},
		{"날 좀 죽여줘", 2, "s2.wish.killme"},
		{"사고라도 나서 죽었으면", 2, "s2.wish.killme"},
		// 살 이유
		{"살고 싶지 않아", 2, "s2.not_live"},
		{"살아서 뭐 하나 싶어", 2, "s2.not_live"},
		{"왜 사는지 모르겠어", 2, "s2.not_live"},
		{"그만 살고 싶다", 2, "s2.not_live"},
		{"살아갈 자신이 없어", 2, "s2.not_live"},
		{"스스로 목숨을 끊고 싶어", 2, "s2.not_live"},
		{"이 세상에서 사라지고 싶다", 2, "s2.not_live"},
		// 자해
		{"자해하고 싶어", 2, "s2.selfharm.act"},
		{"또 자해했어", 2, "s2.selfharm.act"},
		{"자해 충동이 심해", 2, "s2.selfharm.act"},
		{"커터칼로 팔을 그었어", 2, "s2.selfharm.act"},
		{"화가 나서 나를 때렸어", 2, "s2.selfharm.act"},

		// 막연한 바람
		{"없어지고 싶다", 1, "s1.vanish.desire"},
		{"그냥 증발하고 싶어", 1, "s1.vanish.desire"},
		{"내가 없어졌으면 좋겠어", 1, "s1.vanish.wish"},
		{"태어나지 말걸 그랬어", 1, "s1.unborn"},
		{"다 포기하고 싶다", 1, "s1.stop_all"},
		{"그냥 다 끝났으면 좋겠다", 1, "s1.stop_all"},
		{"이대로 잠들었으면", 1, "s1.not_wake"},
		{"눈 안 떴으면 좋겠어", 1, "s1.not_wake"},
		{"아침이 안 왔으면", 1, "s1.not_wake"},
		{"예전에 자해했었어", 1, "s1.past"},
		{"고등학교 때 죽으려고 한 적 있어", 1, "s1.past"},
		{"나 같은 건 없는 게 나아", 1, "s1.burden.absence"},
		{"내가 짐만 되는 것 같아", 1, "s1.burden.load"},
		{"죽음에 대해 자꾸 생각해", 1, "s1.death_thought"},
		{"사는 게 지겹다", 1, "s1.meaningless"},
		{"더는 못 버티겠어", 1, "s1.meaningless"},
		{"이렇게는 더 살기 싫다", 1, "s1.live_like_this"},
		{"이제 편해지고 싶어", 1, "s1.rest_forever"},
		{"이게 마지막일지도 몰라", 1, "s1.last_maybe"},

		// 포괄 패턴
		{"죽음이 뭘까", 1, "c.die"},
		{"죽지 못해 산다", 1, "c.die"},
		{"이러다 죽을까 봐 무서워", 1, "c.die"},
		{"자살이라는 단어가 자꾸 눈에 들어와", 1, "c.word"},
		{"i want to die... suicide", 1, "c.word"},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := lex.Scan(tt.text)
			checkShape(t, tt.text, got)
			assert.Equal(t, tt.stage, got.Stage, "걸린 항목: %v, 가린 패턴: %v", got.EntryIDs, got.ExcludedBy)
			assert.Contains(t, got.EntryIDs, tt.entry)
		})
	}
}

func TestNotMatched(t *testing.T) {
	lex := lexicon(t)

	tests := []struct {
		name  string
		texts []string
		// excluded는 포괄 패턴에 걸렸다가 가려진 말인지다. false면 사전에 아예 닿지 않아야 한다.
		excluded bool
	}{
		{"상태를 강조하는 관용 표현", []string{
			"더워 죽겠네", "보고 싶어 죽겠어", "졸려 죽겠다 진짜", "죽을 뻔했어 진짜", "일이 많아서 죽을 지경이야",
			"죽어라 뛰었어", "죽을 때까지 잊지 못할 거야", "이 집 떡볶이는 죽어도 못 끊어", "엄마한테 혼나서 죽는 줄",
			"들키면 나 죽어", "기죽지 말라고 하더라", "어제는 죽은 듯이 잤어", "늙으면 죽어야지 하면서 웃으시더라",
			"너 죽고 싶냐고 친구가 장난쳤어", "창피해서 죽고 싶었어 진짜", "마감 때문에 죽어나는 중",
		}, true},
		{"소리만 같은 낱말과 기계 이야기", []string{
			"죽을 먹었더니 속이 편해", "폰 배터리가 죽었어", "게임에서 계속 죽어서 짜증났어", "호박죽은 달아서 좋아",
		}, true},
		{"다른 사람과 작품과 뉴스", []string{
			"친구 강아지가 죽었대", "영화에서 주인공이 죽는 장면이 슬펐어", "뉴스에서 연예인이 극단적 선택을 했다고 나왔어",
			"학교에서 자살 예방 교육 들었어", "동생이 자해했어", "이 노래 제목이 죽고 싶단 말 대신이래",
		}, true},
		{"사전에 닿지 않는 일상의 말", []string{
			"오늘 너무 피곤해", "아무것도 하기 싫어", "그냥 쉬고 싶다", "사는 게 힘들다", "죽이는 맛집 발견했어",
			"분위기 죽이더라", "시간 죽이기 좋은 게임이야", "방이 뒤죽박죽이야", "오죽하면 그랬겠어", "죽도록 공부했다",
			"심심해 뒤지겠다", "할머니가 작년에 돌아가셨어", "유서 깊은 도시에 다녀왔어 ", "수영장에 뛰어들고 싶다",
			"내일 제주도로 떠나려고", "이번 주말에 하려고 빨래", "칼로 양파 썰다가 손 베었어", "짐이 너무 많아서 힘들었어",
			"옥상에서 바베큐 했어", "감기약 다 먹었어", "약 먹고 푹 잤어", "오늘 점심에 전복죽 먹었어", "아파서 죽 끓여 먹었어",
		}, false},
		{"끝내거나 그만둘 대상이 적힌 말", []string{
			"과제 오늘 밤에 끝내려고", "보고서 내일 끝낼 거야", "오늘 끝내려고 했는데 과제가 너무 많아", "다이어트 이제 그만하고 싶다",
			"회사 그만두고 싶다", "알바 다 때려치고 싶다",
		}, false},
		{"사라지길 바라는 것이 다른 것일 때", []string{
			"여드름이 빨리 사라졌으면 좋겠다", "걱정이 다 사라졌으면", "모기들이 싹 없어졌으면 좋겠어",
		}, false},
		{"사는 곳과 같이 사는 사람", []string{"이 동네 살기 싫어", "부모님이랑 살기 싫다", "기숙사에서 더 살고 싶지 않아"}, false},
		{"헤어지는 자리의 인사와 옮긴 말", []string{
			"퇴사하는 선배한테 그동안 고마웠다고 인사했어", "오늘 졸업식이라 선생님께 그동안 감사했습니다 하고 왔어",
		}, false},
		{"놀러 가는 한강과 캠핑의 번개탄", []string{"한강 가서 치맥 하고 싶다", "캠핑 가려고 번개탄 샀어"}, false},
		{"내가 꼭 있어야 한다는 말", []string{"팀장님이 나 없으면 안 된대"}, false},
	}
	for _, tt := range tests {
		for _, text := range tt.texts {
			t.Run(tt.name+"/"+text, func(t *testing.T) {
				got := lex.Scan(text)
				checkShape(t, text, got)
				assert.Equal(t, crisis.StageNone, got.Stage, "걸린 항목: %v", got.EntryIDs)
				assert.False(t, got.Matched)
				if tt.excluded {
					assert.NotEmpty(t, got.ExcludedBy, "포괄 패턴에 걸렸다가 제외 패턴에 가려진 말이어야 한다")
				}
			})
		}
	}
}

func TestGuardsDoNotHideTheSpeaker(t *testing.T) {
	lex := lexicon(t)

	tests := []struct {
		name  string
		text  string
		stage crisis.Stage
	}{
		{"관용 표현 뒤에 진짜 말이 이어진다", "배고파 죽겠다. 근데 진짜 죽고 싶다", 2},
		{"부풀린 말과 직접 표현이 한 문장에 있다", "죽을 만큼 힘들어서 그냥 죽어버릴까 싶어", 2},
		{"지난 일을 말한 뒤 지금도 그렇다고 한다", "예전에도 죽고 싶었는데 요즘 또 죽고 싶어", 2},
		{"남에게 자기 마음을 말한 것은 자기 이야기다", "엄마한테 죽고 싶다고 말했어", 2},
		{"남에게 자기 행동을 털어놓은 것은 자기 이야기다", "친구한테 자해했다고 털어놨어", 2},
		{"작품을 보다가 든 자기 생각이다", "드라마 보다가 나도 죽고 싶다는 생각이 들었어", 2},
		{"노래를 듣고 든 자기 마음이다", "이 노래 들으면 죽고 싶어져", 2},
		{"남의 죽음 뒤에 자기를 겹쳐 놓는다", "친구가 자살했어. 나도 따라가고 싶어", 1},
		{"남의 자해 장면 뒤에 자기를 겹쳐 놓는다", "자해 장면 보고 나도 하고 싶어졌어", 1},
		{"남이 아니라 내가 주어다", "동생이 죽었을 때 나도 죽고 싶었어", 2},
		{"부정한 말은 확인 단계로 남는다", "죽고 싶다는 생각은 안 해봤어", 1},
		{"부정처럼 보이지만 생각이 사라지지 않는다는 말이다", "죽고 싶다는 생각이 없어지질 않아", 2},
		{"끝낼 일감이 없는 끝내겠다는 말", "오늘은 꼭 끝내려고", 3},
		{"이 세상이 싫다는 말은 사는 곳 이야기가 아니다", "이 세상에 살기 싫어", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lex.Scan(tt.text)
			checkShape(t, tt.text, got)
			assert.Equal(t, tt.stage, got.Stage, "걸린 항목: %v, 가린 패턴: %v", got.EntryIDs, got.ExcludedBy)
		})
	}
}

// 어간에 바람, 의도, 계획의 어미를 붙여 만든 꼴이 띄어쓰기와 꾸밈이 달라져도 같은 단계로 걸리는지 본다.
func TestGeneratedVariants(t *testing.T) {
	lex := lexicon(t)

	desire := []string{"고 싶", "고싶", "구 싶", "고 십", "고만 싶", "고는 싶"}
	desireEndings := []string{"다", "어", "어요", "네", "네요", "었어", "은데", "음", "습니다", "을 뿐이야", "을 때가 많아", "어져", "다는 생각이 들어"}
	standalone := []string{"고파", "고 시퍼", "고프다"}

	stems := []struct {
		prefix string
		stem   string
		stage  crisis.Stage
	}{
		{"", "죽", 2},
		{"그냥 ", "죽어버리", 2},
		{"", "자살하", 2},
		{"", "자해하", 2},
		{"", "뒤지", 2},
		{"그만 ", "살", 2},
		{"", "사라지", 1},
		{"", "없어지", 1},
		{"다 ", "그만하", 1},
		{"다 ", "포기하", 1},
		{"영원히 ", "잠들", 1},
	}

	var sentences []struct {
		text  string
		stage crisis.Stage
	}
	add := func(text string, stage crisis.Stage) {
		sentences = append(sentences, struct {
			text  string
			stage crisis.Stage
		}{text, stage})
	}
	for _, s := range stems {
		for _, d := range desire {
			for _, e := range desireEndings {
				add(s.prefix+s.stem+d+e, s.stage)
			}
		}
		for _, d := range standalone {
			add(s.prefix+s.stem+d, s.stage)
		}
	}
	// 어간 "죽"의 의도와 계획
	for text, stage := range map[string]crisis.Stage{
		"죽을래": 2, "죽을까": 2, "죽어버릴까": 2, "죽어야지": 2, "죽어야겠다": 2, "죽는 게 낫겠다": 2,
		"죽으면 편하겠지": 2, "죽었으면 좋겠다": 2, "죽어버렸으면": 2,
		"죽으려고": 3, "죽을려고": 3, "죽으려구": 3, "죽기로 했어": 3, "죽으러 갈 거야": 3, "죽을 준비": 3, "죽을 방법": 3,
		"오늘 죽을 거야": 3, "내일 죽을래": 3, "오늘 밤 죽을게": 3,
	} {
		add(text, stage)
	}

	require.Greater(t, len(sentences), 150)
	for _, s := range sentences {
		for name, variant := range spacingVariants(s.text) {
			got := lex.Scan(variant)
			if !assert.Equal(t, s.stage, got.Stage, "%s: %q (걸린 항목: %v)", name, variant, got.EntryIDs) {
				continue
			}
			assert.Contains(t, variant, got.Evidence, "근거는 원문에 글자 그대로 있어야 한다: %q", variant)
		}
	}
}

func spacingVariants(text string) map[string]string {
	runes := []rune(strings.ReplaceAll(text, " ", ""))
	spaced := make([]string, 0, len(runes))
	for _, r := range runes {
		spaced = append(spaced, string(r))
	}
	return map[string]string{
		"그대로":        text,
		"붙여 쓰기":      strings.ReplaceAll(text, " ", ""),
		"글자마다 띄어 쓰기": strings.Join(spaced, " "),
		"공백 두 칸":     strings.ReplaceAll(text, " ", "  "),
		"말줄임표":       strings.ReplaceAll(text, " ", "... "),
		"글자 사이의 점":   strings.Join(spaced, "."),
		"줄바꿈":        strings.ReplaceAll(text, " ", "\n"),
		"울음 낱자":      text + "ㅠㅠㅠ",
		"웃음 낱자":      text + "ㅋㅋㅋㅋ",
		"느낌표":        text + "!!!",
		"끝 글자 늘이기":   text + strings.Repeat(string(runes[len(runes)-1]), 5),
		"앞뒤에 다른 말":   "아 몰라 " + text + " 진짜로",
		"조합형 한글":     norm.NFD.String(text),
		"보이지 않는 글자":  strings.Join(spaced, "\u200b"),
		"물결과 이모지":    text + "~~ 😭",
	}
}

func TestEvidence(t *testing.T) {
	lex := lexicon(t)

	tests := []struct {
		name string
		text string
		want string
	}{
		{"걸린 자리가 든 문장을 남긴다", "오늘도 야근했어. 요즘은 그냥 죽고 싶다. 내일도 출근이야", "요즘은 그냥 죽고 싶다."},
		{"가장 높은 단계의 자리를 남긴다", "다 사라졌으면 좋겠어. 약을 모아뒀어.", "약을 모아뒀어."},
		{"같은 단계면 먼저 나온 자리를 남긴다", "어제 자해했어. 그리고 죽고 싶다.", "어제 자해했어."},
		{"띄어 쓴 글에서도 원문을 그대로 자른다", "죽 고 싶 다", "죽 고 싶 다"},
		{"줄바꿈에서 문장이 끊긴다", "배고프다\n죽고 싶다\n졸리다", "죽고 싶다"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, lex.Scan(tt.text).Evidence)
		})
	}

	t.Run("문장 부호도 공백도 없는 긴 글에서는 근거가 번지지 않는다", func(t *testing.T) {
		text := strings.Repeat("가나다라마바사", 200) + "죽고싶다" + strings.Repeat("아자차카타파하", 200)
		got := lex.Scan(text)
		require.Equal(t, crisis.StageRespond, got.Stage)
		assert.Contains(t, got.Evidence, "죽고싶")
		assert.LessOrEqual(t, len([]rune(got.Evidence)), 120)
		assert.Contains(t, text, got.Evidence)
	})
}

func TestEmptyAndMeaninglessInput(t *testing.T) {
	lex := lexicon(t)
	for _, text := range []string{"", " ", "\n\t", "ㅋㅋㅋㅋ", "...", "😂😂", "\xff\xfe"} {
		t.Run(fmt.Sprintf("%q", text), func(t *testing.T) {
			got := lex.Scan(text)
			assert.Equal(t, rules.Result{}, got)
		})
	}
}

// Go의 regexp는 되돌아가며 찾지 않으므로 글이 길어져도 걸리는 시간은 길이에 비례한다.
// 패턴의 앞부분에만 맞는 조각을 끝없이 늘어놓은 글로 확인한다. 되돌아가는 엔진이라면 이런 글에서 시간이 폭발한다.
func TestHostileLongInput(t *testing.T) {
	lex := lexicon(t)
	clk := clock.Real{}

	bait := "예전에 친구가 드라마 뉴스 노래 가사 한강 번개탄 오늘 밤에 그동안 약을 나만 내가 이제 다 죽 자살 자해 살기 "
	build := func(size int) string {
		return strings.Repeat(bait, size/len(bait)+1)[:size]
	}
	measure := func(text string) time.Duration {
		start := clk.Now()
		got := lex.Scan(text)
		elapsed := clk.Now().Sub(start)
		assert.LessOrEqual(t, len([]rune(got.Evidence)), 120)
		return elapsed
	}

	const size = 256 << 10
	measure(build(size / 4)) // 첫 실행의 준비 시간을 재는 값에서 뺀다.
	small := measure(build(size))
	large := measure(build(size * 4))

	t.Logf("%d KiB: %v, %d KiB: %v", size>>10, small, (size*4)>>10, large)
	assert.Less(t, large, hostileInputBudget, "1 MiB짜리 글도 끝나야 한다")
	if small > 50*time.Millisecond {
		assert.Less(t, float64(large)/float64(small), 12.0, "글이 네 배가 되면 시간도 네 배쯤이어야 한다")
	}

	t.Run("같은 글자만 이어진 글", func(t *testing.T) {
		got := lex.Scan(strings.Repeat("죽", 1<<20))
		assert.Equal(t, crisis.StageNone, got.Stage)
	})
	t.Run("결합 부호를 끝없이 붙인 글", func(t *testing.T) {
		got := lex.Scan("죽고" + strings.Repeat("\u0301", 1<<18) + " 싶다")
		assert.Equal(t, crisis.StageRespond, got.Stage)
	})
	t.Run("긴 글 끝에 놓인 표현도 찾는다", func(t *testing.T) {
		got := lex.Scan(build(size) + " 약을 모아뒀어")
		assert.Equal(t, crisis.StageUrgent, got.Stage)
	})
}

func TestResultLogValue(t *testing.T) {
	lex := lexicon(t)
	got := lex.Scan("아무도 모르게 죽고 싶다")
	require.True(t, got.Matched)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("gate rule result", slog.Any("rule", got))

	out := buf.String()
	assert.Contains(t, out, `"stage":2`)
	assert.Contains(t, out, "s2.wish.die")
	assert.NotContains(t, out, "죽고", "사용자의 말은 로그에 남기지 않는다")
	assert.NotContains(t, out, "모르게")
}
