package analysis_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/fake"
	"github.com/sirin-interact/tmrlife/server/internal/ai/prompts"
	"github.com/sirin-interact/tmrlife/server/internal/analysis"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/store"
)

// partialAnswer는 넘긴 항목만 담은 답이다. 빠진 키를 어떻게 다루는지 보려는 것이다.
func partialAnswer(t *testing.T, items map[signal.Item]judged) string {
	t.Helper()
	out := make(map[string]judged, len(items))
	for item, j := range items {
		out[item.String()] = j
	}
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	return string(raw)
}

func TestExtractSavesEightRows(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.ended(
		assistant("오늘 하루는 어땠어요?"),
		user("어젯밤에 네 번 깼어. 새벽 네 시에 눈이 떠져서 그냥 누워 있었어"),
		assistant("잠을 설치셨네요. 낮에는 어땠어요?"),
		user("회의에서 말실수해서 계속 내 탓만 하고 있어"),
		assistant("그 일이 계속 마음에 남으셨군요."),
		user("밥은 그래도 세 끼 다 챙겨 먹었어"),
	)
	f.reply(map[signal.Item]judged{
		signal.Sleep:       observed(1, "어젯밤에 네 번 깼어"),
		signal.SelfBlame:   observed(2, "계속 내 탓만 하고 있어"),
		signal.Appetite:    notObserved(3, "밥은 그래도 세 끼 다 챙겨 먹었어"),
		signal.Fatigue:     observedIndirect(1, "그냥 누워 있었어"),
		signal.Interest:    notMentioned(),
		signal.Mood:        notMentioned(),
		signal.Psychomotor: notMentioned(),
	})

	result, err := f.service.Extract(t.Context(), c.target(f.userID))
	require.NoError(t, err)

	require.Equal(t, analysis.OutcomeSaved, result.Outcome)
	assert.Equal(t, 3, result.UserUtterances)
	assert.Zero(t, result.Dropped)
	assert.Zero(t, result.Repaired)
	assert.Equal(t, analysis.Counts{Observed: 3, NotObserved: 1, NotMentioned: 4, Direct: 3, Indirect: 1}, result.Counts)
	assert.Equal(t, analysis.ExtractorVersion(f.service.PromptVersion(), fakeModel), result.ExtractorVersion)
	assert.Equal(t, store.AnalysisDone, f.analysisStatus(c))

	rows := f.assertEightRows(c)
	sleep := rows[signal.Sleep.String()]
	assert.Equal(t, "observed", sleep.status)
	assert.Equal(t, "direct", sleep.explicitness)
	assert.Equal(t, "어젯밤에 네 번 깼어", sleep.evidence)
	require.NotNil(t, sleep.utteranceID)
	assert.Equal(t, c.utteranceIDs[1], *sleep.utteranceID, "근거가 어느 발화에서 왔는지 가리킨다")
	assert.Equal(t, result.ExtractorVersion, sleep.version)

	appetite := rows[signal.Appetite.String()]
	assert.Equal(t, "not_observed", appetite.status)
	assert.Equal(t, "밥은 그래도 세 끼 다 챙겨 먹었어", appetite.evidence, "괜찮았다는 판단에도 근거가 남는다")

	fatigue := rows[signal.Fatigue.String()]
	assert.Equal(t, "indirect", fatigue.explicitness)
	assert.Equal(t, "그냥 누워 있었어", fatigue.evidence, "한 발화의 한 토막만 잘라 남긴다")

	quiet := rows[signal.Interest.String()]
	assert.Equal(t, "not_mentioned", quiet.status)
	assert.Equal(t, "none", quiet.explicitness)
	assert.False(t, quiet.hasEvidence, "언급 없음에는 근거가 없다")
	assert.Nil(t, quiet.utteranceID)
}

func TestExtractVerifiesEvidence(t *testing.T) {
	t.Parallel()

	// 모든 경우가 같은 대화를 쓴다. 사용자가 한 말은 두 줄이고, 상대는 세 번 말한다.
	turns := []turn{
		assistant("오늘 하루는 어땠어요?"),
		user("요즘 통 입맛이 없어. 아침도 건너뛰었어"),
		assistant("입맛이 없으면 하루가 더 처지죠. 잠은 어땠어요?"),
		user("잠은 잘 잤어"),
		assistant("그건 다행이네요."),
	}

	tests := []struct {
		name string
		// judgement는 모델이 appetite 항목에 대해 돌려주는 답이다.
		judgement    judged
		wantStatus   string
		wantEvidence string
		wantReason   string
		wantRepaired int
		wantMismatch int
	}{
		{
			name:         "사용자의 발화에 글자 그대로 있는 근거는 받아 준다",
			judgement:    observed(1, "요즘 통 입맛이 없어"),
			wantStatus:   "observed",
			wantEvidence: "요즘 통 입맛이 없어",
		},
		{
			name:       "지어낸 근거는 버리고 그 항목은 언급 없음이 된다",
			judgement:  observed(1, "며칠째 밥을 한 숟갈도 못 넘겼어"),
			wantStatus: "not_mentioned",
			wantReason: "not_verbatim",
		},
		{
			name:       "상대가 한 말을 근거로 내놓으면 버린다",
			judgement:  observed(1, "입맛이 없으면 하루가 더 처지죠"),
			wantStatus: "not_mentioned",
			wantReason: "not_verbatim",
		},
		{
			name:       "두 발화에 걸친 근거는 받지 않는다",
			judgement:  observed(1, "아침도 건너뛰었어 잠은 잘 잤어"),
			wantStatus: "not_mentioned",
			wantReason: "not_verbatim",
		},
		{
			name:       "한 글자라도 다르면 버린다",
			judgement:  observed(1, "요즘 통 입맛이 없어요"),
			wantStatus: "not_mentioned",
			wantReason: "not_verbatim",
		},
		{
			name:         "앞뒤 공백과 이어진 공백만 다른 근거는 받아 준다",
			judgement:    observed(1, "  요즘   통 입맛이\n없어 "),
			wantStatus:   "observed",
			wantEvidence: "요즘 통 입맛이 없어",
		},
		{
			name:       "근거가 비었으면 버린다",
			judgement:  observed(1, ""),
			wantStatus: "not_mentioned",
			wantReason: "no_evidence",
		},
		{
			name:       "글자도 숫자도 없는 근거는 버린다",
			judgement:  observed(1, ". "),
			wantStatus: "not_mentioned",
			wantReason: "no_letters",
		},
		{
			name:       "정해진 값이 아닌 판단은 그 항목만 버린다",
			judgement:  judged{Status: "관찰됨", Explicitness: "direct", Line: 1, Evidence: "요즘 통 입맛이 없어"},
			wantStatus: "not_mentioned",
			wantReason: "bad_status",
		},
		{
			name:       "정해진 값이 아닌 명시성은 그 항목만 버린다",
			judgement:  judged{Status: "observed", Explicitness: "아주 분명함", Line: 1, Evidence: "요즘 통 입맛이 없어"},
			wantStatus: "not_mentioned",
			wantReason: "bad_explicitness",
		},
		{
			name:         "근거는 맞는데 명시성이 없음이면 간접 추론으로 고친다",
			judgement:    judged{Status: "observed", Explicitness: "none", Line: 1, Evidence: "아침도 건너뛰었어"},
			wantStatus:   "observed",
			wantEvidence: "아침도 건너뛰었어",
			wantRepaired: 1,
		},
		{
			name:         "가리킨 줄이 틀려도 다른 줄에 글자가 있으면 받아 준다",
			judgement:    observed(2, "요즘 통 입맛이 없어"),
			wantStatus:   "observed",
			wantEvidence: "요즘 통 입맛이 없어",
			wantMismatch: 1,
		},
		{
			name:         "있지도 않은 줄을 가리켜도 글자가 맞으면 받아 준다",
			judgement:    observed(9, "요즘 통 입맛이 없어"),
			wantStatus:   "observed",
			wantEvidence: "요즘 통 입맛이 없어",
			wantMismatch: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			c := f.ended(turns...)
			f.reply(map[signal.Item]judged{signal.Appetite: tt.judgement})

			result, err := f.service.Extract(t.Context(), c.target(f.userID))
			require.NoError(t, err)

			require.Equal(t, analysis.OutcomeSaved, result.Outcome, "항목 하나가 버려져도 나머지는 저장한다")
			rows := f.assertEightRows(c)
			appetite := rows[signal.Appetite.String()]
			assert.Equal(t, tt.wantStatus, appetite.status)
			assert.Equal(t, tt.wantEvidence, appetite.evidence)
			assert.Equal(t, tt.wantEvidence != "", appetite.hasEvidence)
			assert.Equal(t, tt.wantRepaired, result.Repaired)
			assert.Equal(t, tt.wantMismatch, result.LineMismatch)
			if tt.wantReason == "" {
				assert.Zero(t, result.Dropped)
				return
			}
			assert.Equal(t, 1, result.Dropped)
			assert.Equal(t, map[string]int{tt.wantReason: 1}, result.DropReasons)
			assert.Nil(t, appetite.utteranceID, "버린 항목은 발화를 가리키지 않는다")
		})
	}
}

// 남의 말을 옮긴 것과 관용 표현은 글자 대조로 가릴 수 없다. 사용자가 실제로 한 말이기 때문이다.
// 그 일은 지시문이 맡고, 실제 모델로 도는 시험(live_test.go)이 확인한다.
// 여기서는 코드가 어디까지 지키는지를 못박아 둔다: 사용자의 발화에서 나온 글자면 저장하고, 아니면 버린다.
func TestExtractCannotCatchQuotedSomeoneElse(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.ended(
		assistant("오늘 하루는 어땠어요?"),
		user("팀장이 입맛이 없다고 하도 징징대서 점심 고르는 데 한참 걸렸어"),
		user("나는 돈가스 먹었어. 오전 내내 배고파 죽는 줄 알았거든"),
	)
	f.reply(map[signal.Item]judged{
		signal.Appetite: observed(1, "팀장이 입맛이 없다고"),
	})

	result, err := f.service.Extract(t.Context(), c.target(f.userID))
	require.NoError(t, err)

	assert.Zero(t, result.Dropped, "사용자가 한 말에서 나온 글자라 대조는 통과한다")
	rows := f.assertEightRows(c)
	assert.Equal(t, "observed", rows[signal.Appetite.String()].status)
}

// 근거 화면은 저장된 발화를 가리켜 그 말을 언제 했는지까지 보여줄 수 있다.
// 그래서 어느 발화에서 왔는지 가릴 수 없는 토막에는 발화를 가리키지 않는다. 엉뚱한 발화를 가리키면
// 화면이 그 말을 한 적 없는 자리에 인용을 붙인다. 판단과 근거 글은 그대로 남으므로 점수는 달라지지 않는다.
func TestExtractLeavesAmbiguousEvidenceUnanchored(t *testing.T) {
	t.Parallel()

	// "어"는 두 발화에 모두 있다. 첫 줄에서는 낱말 하나이고 둘째 줄에서는 "없었어" 안에 들어 있다.
	turns := []turn{
		assistant("어젯밤 잠은 좀 어땠어요?"),
		user("어 자다 깨다 했어"),
		assistant("입맛은 어떠셨어요?"),
		user("점심때까지 입맛이 하나도 없었어"),
	}

	tests := []struct {
		name string
		// judgement는 모델이 appetite 항목에 대해 돌려주는 답이다.
		judgement      judged
		wantUtterance  int
		wantAnchored   bool
		wantMismatch   int
		wantUnanchored int
	}{
		{
			name:          "가리킨 줄에서 찾으면 그 발화를 가리킨다",
			judgement:     observed(2, "어"),
			wantUtterance: 3,
			wantAnchored:  true,
		},
		{
			name:           "가리킨 줄이 빗나가고 그 글자가 여러 줄에 있으면 발화를 가리키지 않는다",
			judgement:      observed(9, "어"),
			wantMismatch:   1,
			wantUnanchored: 1,
		},
		{
			name:          "가리킨 줄이 빗나갔어도 그 글자가 한 줄에만 있으면 그 발화를 가리킨다",
			judgement:     observed(9, "입맛이 하나도 없었어"),
			wantUtterance: 3,
			wantAnchored:  true,
			wantMismatch:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			c := f.ended(turns...)
			f.reply(map[signal.Item]judged{signal.Appetite: tt.judgement})

			result, err := f.service.Extract(t.Context(), c.target(f.userID))
			require.NoError(t, err)

			require.Equal(t, analysis.OutcomeSaved, result.Outcome)
			assert.Zero(t, result.Dropped, "발화를 가리키지 못해도 판단과 근거는 버리지 않는다")
			assert.Equal(t, tt.wantMismatch, result.LineMismatch)
			assert.Equal(t, tt.wantUnanchored, result.Unanchored)

			appetite := f.assertEightRows(c)[signal.Appetite.String()]
			assert.Equal(t, "observed", appetite.status)
			assert.Equal(t, tt.judgement.Evidence, appetite.evidence)
			if !tt.wantAnchored {
				assert.Nil(t, appetite.utteranceID, "어느 발화에서 왔는지 가릴 수 없으면 가리키지 않는다")
				return
			}
			require.NotNil(t, appetite.utteranceID)
			assert.Equal(t, c.utteranceIDs[tt.wantUtterance], *appetite.utteranceID)
		})
	}
}

func TestExtractRejectsAnswer(t *testing.T) {
	t.Parallel()

	t.Run("여덟 키 가운데 하나가 빠지면 답 전체를 버린다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(user("오늘은 아무것도 하기 싫었어"))
		seven := map[signal.Item]judged{}
		for _, item := range signal.AllItems() {
			if item == signal.Psychomotor {
				continue
			}
			seven[item] = notMentioned()
		}
		f.llm.Enqueue(fake.Reply(partialAnswer(t, seven)))

		_, err := f.service.Extract(t.Context(), c.target(f.userID))

		require.ErrorIs(t, err, analysis.ErrRejected)
		assert.Contains(t, err.Error(), "missing_item")
		assert.Empty(t, f.signals(c), "답을 버렸으면 행을 하나도 남기지 않는다")
		assert.Equal(t, store.AnalysisRunning, f.analysisStatus(c), "다시 시도할 실패는 상태를 닫지 않는다")
	})

	t.Run("JSON이 아닌 답은 버린다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(user("오늘은 아무것도 하기 싫었어"))
		f.llm.Enqueue(fake.Reply(`{"interest": "observed"}`))

		_, err := f.service.Extract(t.Context(), c.target(f.userID))

		require.Error(t, err)
		assert.Empty(t, f.signals(c))
	})
}

func TestExtractSkips(t *testing.T) {
	t.Parallel()

	t.Run("사용자가 한 말이 없으면 모델을 부르지 않고 행도 남기지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(assistant("오늘 하루는 어땠어요?"), assistant("다음에 또 얘기해요."))

		result, err := f.service.Extract(t.Context(), c.target(f.userID))
		require.NoError(t, err)

		assert.Equal(t, analysis.OutcomeNothingSaid, result.Outcome)
		assert.Zero(t, f.llm.Calls())
		assert.Empty(t, f.signals(c))
		assert.Equal(t, store.AnalysisNone, f.analysisStatus(c),
			"말이 없던 대화를 분석이 끝난 대화로 두면 그날이 대화한 날로 세어져 점수가 낮아진다")
	})

	t.Run("분석을 꺼 두면 뽑지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(user("어젯밤에 한숨도 못 잤어"))
		f.setAnalysisEnabled(false)

		result, err := f.service.Extract(t.Context(), c.target(f.userID))
		require.NoError(t, err)

		assert.Equal(t, analysis.OutcomeDisabled, result.Outcome)
		assert.Zero(t, f.llm.Calls())
		assert.Empty(t, f.signals(c))
		assert.Equal(t, store.AnalysisNone, f.analysisStatus(c))
	})

	t.Run("아직 열려 있는 대화는 뽑지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("어젯밤에 한숨도 못 잤어"))

		_, err := f.service.Extract(t.Context(), c.target(f.userID))

		require.ErrorIs(t, err, analysis.ErrStillActive)
		assert.Zero(t, f.llm.Calls())
		assert.Equal(t, store.AnalysisNone, f.analysisStatus(c))
	})

	t.Run("지워진 대화는 결과로 알린다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		result, err := f.service.Extract(t.Context(), analysis.Target{UserID: f.userID, ConversationID: newID(t)})
		require.NoError(t, err)

		assert.Equal(t, analysis.OutcomeGone, result.Outcome)
		assert.Zero(t, f.llm.Calls())
	})

	t.Run("남의 대화는 뽑지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(user("어젯밤에 한숨도 못 잤어"))

		result, err := f.service.Extract(t.Context(), analysis.Target{UserID: newID(t), ConversationID: c.id})
		require.NoError(t, err)

		assert.Equal(t, analysis.OutcomeGone, result.Outcome, "설정이 없는 사용자다")
		assert.Empty(t, f.signals(c))
	})
}

// 위기 대응이 있었던 대화에서 일기 초안은 만들지 않지만 신호는 뽑는다.
// 그 대화가 그날의 유일한 대화였으면, 뽑지 않는 쪽을 골랐을 때 그날이 추세에서 통째로 사라진다.
func TestExtractAnalysesCrisisConversation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open(
		assistant("오늘 하루는 어땠어요?"),
		user("요즘 다 사라졌으면 좋겠다는 생각이 들어"),
		assistant("지금 많이 힘드신 것 같아요. 혼자 견디지 않으셔도 돼요."),
		user("잠도 잘 안 오고 아무것도 재미가 없어"),
	)
	f.gate(c, 1, 3)
	f.end(c)
	f.reply(map[signal.Item]judged{
		signal.Sleep:    observed(2, "잠도 잘 안 오고"),
		signal.Interest: observed(2, "아무것도 재미가 없어"),
	})

	result, err := f.service.Extract(t.Context(), c.target(f.userID))
	require.NoError(t, err)

	require.Equal(t, analysis.OutcomeSaved, result.Outcome)
	rows := f.assertEightRows(c)
	assert.Equal(t, "observed", rows[signal.Sleep.String()].status)
	assert.Equal(t, "observed", rows[signal.Interest.String()].status)
	assert.Equal(t, "not_mentioned", rows[signal.Mood.String()].status)
}

// 자해와 죽음에 관한 표현은 여덟 항목이 아니다. 관문에 걸린 발화는 근거가 될 수 없고,
// 그 줄에서만 찾히는 근거를 내놓은 항목은 언급 없음으로 되돌린다.
func TestExtractRefusesGateFlaggedEvidence(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open(
		assistant("오늘 하루는 어땠어요?"),
		user("요즘 다 사라졌으면 좋겠다는 생각이 들어"),
		assistant("지금 많이 힘드신 것 같아요."),
		user("잠도 잘 안 오고 아무것도 재미가 없어"),
	)
	f.gate(c, 1, 1) // 확인 단계만으로도 근거가 될 수 없다
	f.end(c)
	f.reply(map[signal.Item]judged{
		signal.Mood:  observed(1, "다 사라졌으면 좋겠다는 생각이 들어"),
		signal.Sleep: observed(2, "잠도 잘 안 오고"),
	})

	result, err := f.service.Extract(t.Context(), c.target(f.userID))
	require.NoError(t, err)

	require.Equal(t, analysis.OutcomeSaved, result.Outcome)
	assert.Equal(t, 1, result.Dropped)
	assert.Equal(t, map[string]int{"gate_flagged": 1}, result.DropReasons)

	rows := f.assertEightRows(c)
	mood := rows[signal.Mood.String()]
	assert.Equal(t, "not_mentioned", mood.status)
	assert.False(t, mood.hasEvidence)
	assert.Equal(t, "observed", rows[signal.Sleep.String()].status, "같은 대화의 다른 말로 드러난 항목은 그대로 뽑는다")
	assert.Equal(t, "잠도 잘 안 오고", rows[signal.Sleep.String()].evidence)
	assert.Contains(t, f.sentText(0), "사라졌으면", "걸린 발화도 흐름을 알 수 있게 모델에는 보낸다")
}

func TestExtractIsIdempotent(t *testing.T) {
	t.Parallel()

	t.Run("두 번 돌려도 행이 늘지 않고 모델을 다시 부르지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(user("어젯밤에 두 시간밖에 못 잤어"))
		f.reply(map[signal.Item]judged{signal.Sleep: observed(1, "두 시간밖에 못 잤어")})

		first, err := f.service.Extract(t.Context(), c.target(f.userID))
		require.NoError(t, err)
		require.Equal(t, analysis.OutcomeSaved, first.Outcome)
		saved := f.signals(c)

		second, err := f.service.Extract(t.Context(), c.target(f.userID))
		require.NoError(t, err)

		assert.Equal(t, analysis.OutcomeAlreadyDone, second.Outcome)
		assert.Equal(t, 1, f.llm.Calls(), "이미 끝난 대화에는 모델을 부르지 않는다")
		assert.Equal(t, saved, f.signals(c), "먼저 들어간 행이 한 글자도 바뀌지 않는다")
		assert.Equal(t, store.AnalysisDone, f.analysisStatus(c))
	})

	t.Run("맡아 둔 채로 멈춘 대화는 이어받는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(user("어젯밤에 두 시간밖에 못 잤어"))
		_, err := f.store.Queries().ClaimConversationAnalysis(t.Context(), claimParams(f, c))
		require.NoError(t, err)
		require.Equal(t, store.AnalysisRunning, f.analysisStatus(c))
		f.reply(map[signal.Item]judged{signal.Sleep: observed(1, "두 시간밖에 못 잤어")})

		result, err := f.service.Extract(t.Context(), c.target(f.userID))
		require.NoError(t, err)

		assert.Equal(t, analysis.OutcomeSaved, result.Outcome)
		assert.True(t, result.Takeover)
		f.assertEightRows(c)
	})

	t.Run("맡은 뒤에 다른 실행이 먼저 저장했으면 그 행을 그대로 둔다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(user("어젯밤에 두 시간밖에 못 잤어"))
		reply := answer(t, map[signal.Item]judged{signal.Sleep: observed(1, "두 시간밖에 못 잤어")})

		// 모델을 부르는 동안 다른 실행이 끝난 상황이다. 그 실행은 제 모델을 쓴다.
		otherLLM := fake.New(fakeModel)
		otherLLM.Enqueue(fake.Reply(reply))
		other := f.newService(func(o *analysis.Options) { o.LLM = otherLLM })
		var otherOutcome analysis.Outcome
		var otherErr error
		f.llm.SetHandler(func(_ context.Context, _ ai.Request) fake.Step {
			var got analysis.Result
			got, otherErr = other.Extract(context.WithoutCancel(t.Context()), c.target(f.userID))
			otherOutcome = got.Outcome
			return fake.Reply(reply)
		})

		result, err := f.service.Extract(t.Context(), c.target(f.userID))
		require.NoError(t, err)
		require.NoError(t, otherErr)
		require.Equal(t, analysis.OutcomeSaved, otherOutcome, "먼저 끝낸 쪽이 여덟 행을 저장한다")

		assert.Equal(t, analysis.OutcomeAlreadyDone, result.Outcome)
		f.assertEightRows(c)
	})
}

func TestExtractSendsTranscript(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.ended(
		assistant("오늘 하루는 어땠어요?"),
		user("그냥 그래.\n별일 없었어"),
		assistant("그런 날도 있죠."),
		user("응"),
	)
	f.reply(nil)

	_, err := f.service.Extract(t.Context(), c.target(f.userID))
	require.NoError(t, err)

	sent := f.sentText(0)
	assert.Equal(t, strings.Join([]string{
		"대화 기록:",
		"[상대] 오늘 하루는 어땠어요?",
		"1. 그냥 그래. 별일 없었어",
		"[상대] 그런 날도 있죠.",
		"2. 응",
	}, "\n"), sent, "사용자의 말에만 번호가 붙고, 줄바꿈은 한 칸으로 줄어든다")

	req, ok := f.llm.LastRequest()
	require.True(t, ok)
	assert.NotEmpty(t, req.JSONSchema, "답은 정해진 꼴의 JSON으로 받는다")
}

// 지시문은 코드가 만드는 입력과 같은 낱말로 입력을 설명해야 한다. 한쪽만 고치면 모델이 엉뚱한 자리를 근거로 삼는다.
func TestPromptMatchesCode(t *testing.T) {
	t.Parallel()
	registry, err := prompts.LoadEmbedded()
	require.NoError(t, err)
	prompt, err := registry.Get(analysis.PromptTask)
	require.NoError(t, err)

	for _, label := range analysis.PromptLabels() {
		assert.Contains(t, prompt.System, strings.TrimSpace(label))
	}
	for _, item := range signal.AllItems() {
		assert.Contains(t, prompt.System, item.String(), "지시문에 항목 키가 있어야 한다")
		assert.Contains(t, string(prompt.Schema), item.String(), "스키마에 항목 키가 있어야 한다")
	}
	for _, value := range []string{"observed", "not_observed", "not_mentioned", "direct", "indirect", "none"} {
		assert.Contains(t, prompt.System, value)
	}
	assert.Contains(t, prompt.System, "evidence")
	assert.Contains(t, prompt.System, "line")
}
