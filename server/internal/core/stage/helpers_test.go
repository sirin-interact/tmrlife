package stage

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
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

// times는 같은 줄을 n일 동안 되풀이한다.
func times(n int, line string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = line
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

// stagesOf는 흐름에서 단계만 뽑는다.
func stagesOf(series []Point) []Stage {
	out := make([]Stage, 0, len(series))
	for _, pt := range series {
		out = append(out, pt.Stage)
	}
	return out
}

// at은 그 날짜의 Point를 꺼낸다. 흐름에 없는 날짜면 시험을 멈춘다.
func at(t *testing.T, r Result, date string) Point {
	t.Helper()
	pt, ok := r.At(mustDate(t, date))
	require.True(t, ok, "흐름에 있는 날짜여야 한다")
	return pt
}
