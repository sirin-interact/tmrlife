package assess

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/core/stage"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

func mustDate(t *testing.T, s string) recorddate.Date {
	t.Helper()
	d, err := recorddate.Parse(s)
	require.NoError(t, err)
	return d
}

// diary는 첫날부터 하루에 한 줄씩 적은 기록을 하루의 목록으로 옮긴다.
//
// 한 줄은 여덟 글자이고 자리는 항목 순서다: 흥미 저하, 우울감, 수면, 피로, 식욕, 자기 비난, 집중 곤란, 느려짐·초조.
//
//	O 관찰됨(직접 언급)   o 관찰됨(간접 추론)   x 관찰되지 않음(직접 언급)   . 언급 없음
//
// 빈 줄("")은 대화하지 않은 날이다. 그런 날은 목록에 들어가지 않는다.
func diary(t *testing.T, first string, lines ...string) []signal.Day {
	t.Helper()
	start := mustDate(t, first)

	var days []signal.Day
	for offset, line := range lines {
		if line == "" {
			continue
		}
		require.Len(t, line, signal.ItemCount, "한 줄은 항목 수만큼의 글자여야 한다")

		day := signal.Day{Date: start.AddDays(offset)}
		for idx, mark := range line {
			switch mark {
			case 'O':
				day.Judgements[idx] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Direct}
			case 'o':
				day.Judgements[idx] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Indirect}
			case 'x':
				day.Judgements[idx] = signal.Judgement{Status: signal.NotObserved, Explicitness: signal.Direct}
			case '.':
			default:
				require.FailNow(t, "기록 줄에 쓸 수 없는 글자다")
			}
		}
		days = append(days, day)
	}
	return days
}

// rowsOf는 diary와 같은 글자로 적은 기록을 날짜별 신호 행으로 옮긴다. 하루에 대화 하나가 있었던 것으로 둔다.
// 언급 없음(.)은 행을 만들지 않는다. 그래서 한 줄에 글자가 하나는 있어야 그날이 대화한 날이 된다.
func rowsOf(t *testing.T, first string, lines ...string) map[recorddate.Date][]signal.Row {
	t.Helper()

	rows := map[recorddate.Date][]signal.Row{}
	for _, day := range diary(t, first, lines...) {
		for _, item := range signal.AllItems() {
			judgement := day.Judgement(item)
			if judgement.Status == signal.NotMentioned {
				continue
			}
			rows[day.Date] = append(rows[day.Date], signal.Row{
				ConversationID: "conversation-" + day.Date.String(),
				Item:           item,
				Status:         judgement.Status,
				Explicitness:   judgement.Explicitness,
			})
		}
		require.NotEmpty(t, rows[day.Date], "행이 하나도 없는 날은 대화하지 않은 날이 된다")
	}
	return rows
}

// times는 같은 줄을 n일 동안 되풀이한다.
func times(n int, line string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = line
	}
	return out
}

// weeks는 이레치 기록을 n주 동안 되풀이한다.
func weeks(n int, week ...string) []string {
	out := make([]string, 0, n*len(week))
	for range n {
		out = append(out, week...)
	}
	return out
}

// silence는 대화하지 않은 날 n일이다.
func silence(n int) []string {
	return times(n, "")
}

// join은 여러 구간을 차례로 이어 붙인다.
func join(parts ...[]string) []string {
	var out []string
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

// evaluate는 기본 조정 값으로 평가한다.
func evaluate(t *testing.T, days []signal.Day, asOf string) Evaluation {
	t.Helper()
	return evaluateWith(t, days, asOf, params.Default())
}

func evaluateWith(t *testing.T, days []signal.Day, asOf string, p params.Params) Evaluation {
	t.Helper()
	got, err := Evaluate(days, mustDate(t, asOf), p)
	require.NoError(t, err)
	return got
}

// stageOn은 그 날짜의 개입 단계 기록을 꺼낸다. 흐름에 없는 날짜면 시험을 멈춘다.
func stageOn(t *testing.T, e Evaluation, date string) stage.Point {
	t.Helper()
	pt, ok := e.Stage.At(mustDate(t, date))
	require.True(t, ok, "흐름에 있는 날짜여야 한다")
	return pt
}

// stageChanges는 단계가 바뀐 날만 뽑아 단계의 흐름을 돌려준다. 첫날의 단계에서 시작한다.
func stageChanges(series []stage.Point) []stage.Stage {
	var out []stage.Stage
	for i, pt := range series {
		if i == 0 || pt.Stage != series[i-1].Stage {
			out = append(out, pt.Stage)
		}
	}
	return out
}

// firstDateAt은 그 단계가 처음 나온 날짜다. 없으면 빈 날짜다.
func firstDateAt(series []stage.Point, want stage.Stage) recorddate.Date {
	for _, pt := range series {
		if pt.Stage == want {
			return pt.Date
		}
	}
	return recorddate.Date{}
}

// firstDetected는 변화 감지가 처음 울린 날짜다. 없으면 빈 날짜다.
func firstDetected(series []stage.Point) recorddate.Date {
	for _, pt := range series {
		if pt.Detected {
			return pt.Date
		}
	}
	return recorddate.Date{}
}
