package signal

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

func mustDate(t *testing.T, s string) recorddate.Date {
	t.Helper()
	d, err := recorddate.Parse(s)
	require.NoError(t, err)
	return d
}

func row(conversationID string, item Item, status Status, explicitness Explicitness) Row {
	return Row{ConversationID: conversationID, Item: item, Status: status, Explicitness: explicitness}
}

func cancelled(r Row) Row {
	r.Cancelled = true
	return r
}

// dayWith는 적어 준 항목만 채운 하루를 만든다. 나머지는 언급 없음이다.
func dayWith(date recorddate.Date, judgements map[Item]Judgement) Day {
	day := Day{Date: date}
	for item, j := range judgements {
		day.Judgements[item.Index()] = j
	}
	return day
}

func TestMergeDay(t *testing.T) {
	date := mustDate(t, "2026-10-03")

	observedDirect := Judgement{Status: Observed, Explicitness: Direct}
	observedIndirect := Judgement{Status: Observed, Explicitness: Indirect}
	notObservedDirect := Judgement{Status: NotObserved, Explicitness: Direct}
	notObservedIndirect := Judgement{Status: NotObserved, Explicitness: Indirect}

	tests := []struct {
		name string
		rows []Row
		// want에 없는 항목은 "언급 없음, 근거 없음"이어야 한다.
		want         map[Item]Judgement
		wantObserved int
	}{
		{
			name: "대화 하나의 판단은 그대로 그날의 판단이다",
			rows: []Row{
				// "새벽 4시까지 뒤척이다가 겨우 잤어", "게임도 켜놓고 그냥 껐어, 재미가 없더라"
				row("c1", Sleep, Observed, Direct),
				row("c1", Interest, Observed, Direct),
				row("c1", Concentration, NotMentioned, None),
			},
			want:         map[Item]Judgement{Sleep: observedDirect, Interest: observedDirect},
			wantObserved: 2,
		},
		{
			name: "어떤 행에도 나오지 않은 항목은 언급 없음이다",
			rows: []Row{
				row("c1", Appetite, NotObserved, Direct),
			},
			want:         map[Item]Judgement{Appetite: notObservedDirect},
			wantObserved: 0,
		},
		{
			name: "여덟 항목이 모두 언급 없음이어도 대화한 하루다",
			rows: []Row{
				row("c1", Interest, NotMentioned, None),
				row("c1", Mood, NotMentioned, None),
				row("c1", Sleep, NotMentioned, None),
				row("c1", Fatigue, NotMentioned, None),
				row("c1", Appetite, NotMentioned, None),
				row("c1", SelfBlame, NotMentioned, None),
				row("c1", Concentration, NotMentioned, None),
				row("c1", Psychomotor, NotMentioned, None),
			},
			want:         map[Item]Judgement{},
			wantObserved: 0,
		},

		// 하루에 여러 번 대화한 날: 한 번이라도 관찰되면 관찰됨이다.
		{
			name: "아침에는 관찰되지 않음, 밤에는 관찰됨이면 관찰됨이다",
			rows: []Row{
				row("morning", Sleep, NotObserved, Direct),
				row("night", Sleep, Observed, Direct),
			},
			want:         map[Item]Judgement{Sleep: observedDirect},
			wantObserved: 1,
		},
		{
			name: "아침에 관찰됨이면 밤에 관찰되지 않음이어도 관찰됨이다",
			rows: []Row{
				row("morning", Sleep, Observed, Direct),
				row("night", Sleep, NotObserved, Direct),
			},
			want:         map[Item]Judgement{Sleep: observedDirect},
			wantObserved: 1,
		},
		{
			name: "관찰됨은 언급 없음보다 앞선다",
			rows: []Row{
				row("c1", Mood, NotMentioned, None),
				row("c2", Mood, Observed, Indirect),
			},
			want:         map[Item]Judgement{Mood: observedIndirect},
			wantObserved: 1,
		},
		{
			name: "관찰되지 않음은 언급 없음보다 앞선다",
			rows: []Row{
				row("c1", Fatigue, NotObserved, Indirect),
				row("c2", Fatigue, NotMentioned, None),
			},
			want:         map[Item]Judgement{Fatigue: notObservedIndirect},
			wantObserved: 0,
		},
		{
			name: "두 대화 모두 언급 없음이면 언급 없음이다",
			rows: []Row{
				row("c1", Psychomotor, NotMentioned, None),
				row("c2", Psychomotor, NotMentioned, None),
			},
			want:         map[Item]Judgement{},
			wantObserved: 0,
		},
		{
			name: "세 대화에 세 판단이 다 나오면 관찰됨이다",
			rows: []Row{
				row("c1", SelfBlame, NotMentioned, None),
				row("c2", SelfBlame, NotObserved, Direct),
				row("c3", SelfBlame, Observed, Indirect),
			},
			want:         map[Item]Judgement{SelfBlame: observedIndirect},
			wantObserved: 1,
		},

		// 명시성: 그날의 판단으로 뽑힌 쪽의 행만 본다.
		{
			name: "관찰된 행 가운데 하나라도 직접 언급이면 직접 언급이다",
			rows: []Row{
				row("c1", Mood, Observed, Indirect),
				row("c2", Mood, Observed, Direct),
			},
			want:         map[Item]Judgement{Mood: observedDirect},
			wantObserved: 1,
		},
		{
			name: "직접 언급이 먼저 나와도 결과는 같다",
			rows: []Row{
				row("c1", Mood, Observed, Direct),
				row("c2", Mood, Observed, Indirect),
			},
			want:         map[Item]Judgement{Mood: observedDirect},
			wantObserved: 1,
		},
		{
			name: "관찰된 행이 모두 간접 추론이면 간접 추론이다",
			rows: []Row{
				row("c1", Mood, Observed, Indirect),
				row("c2", Mood, Observed, Indirect),
			},
			want:         map[Item]Judgement{Mood: observedIndirect},
			wantObserved: 1,
		},
		{
			name: "관찰되지 않음의 직접 언급은 관찰됨의 명시성을 올려 주지 않는다",
			rows: []Row{
				row("c1", Sleep, NotObserved, Direct),
				row("c2", Sleep, Observed, Indirect),
			},
			want:         map[Item]Judgement{Sleep: observedIndirect},
			wantObserved: 1,
		},
		{
			name: "관찰되지 않음끼리도 하나라도 직접 언급이면 직접 언급이다",
			rows: []Row{
				row("c1", Appetite, NotObserved, Indirect),
				row("c2", Appetite, NotObserved, Direct),
			},
			want:         map[Item]Judgement{Appetite: notObservedDirect},
			wantObserved: 0,
		},

		// 취소: 행은 남아 있지만 계산에서는 빠진다.
		{
			name: "취소된 관찰됨만 있으면 그 항목은 언급 없음과 같다",
			rows: []Row{
				cancelled(row("c1", Sleep, Observed, Direct)),
				row("c1", Mood, Observed, Direct),
			},
			want:         map[Item]Judgement{Mood: observedDirect},
			wantObserved: 1,
		},
		{
			name: "취소된 관찰됨을 빼면 다른 대화의 관찰되지 않음이 남는다",
			rows: []Row{
				cancelled(row("c1", Sleep, Observed, Direct)),
				row("c2", Sleep, NotObserved, Indirect),
			},
			want:         map[Item]Judgement{Sleep: notObservedIndirect},
			wantObserved: 0,
		},
		{
			name: "취소된 직접 언급은 남은 관찰됨의 명시성을 올려 주지 않는다",
			rows: []Row{
				cancelled(row("c1", Fatigue, Observed, Direct)),
				row("c2", Fatigue, Observed, Indirect),
			},
			want:         map[Item]Judgement{Fatigue: observedIndirect},
			wantObserved: 1,
		},
		{
			name: "취소된 관찰되지 않음도 뺀다",
			rows: []Row{
				cancelled(row("c1", Concentration, NotObserved, Direct)),
				row("c2", Concentration, NotMentioned, None),
			},
			want:         map[Item]Judgement{},
			wantObserved: 0,
		},
		{
			name: "행이 모두 취소되어도 대화한 하루이고, 여덟 항목이 모두 언급 없음이다",
			rows: []Row{
				cancelled(row("c1", Sleep, Observed, Direct)),
				cancelled(row("c1", Mood, Observed, Indirect)),
			},
			want:         map[Item]Judgement{},
			wantObserved: 0,
		},
		{
			name: "두 대화에서 여덟 항목이 모두 관찰된 날",
			rows: []Row{
				row("c1", Interest, Observed, Direct),
				row("c1", Mood, Observed, Direct),
				row("c1", Sleep, Observed, Direct),
				row("c1", Fatigue, Observed, Direct),
				row("c2", Appetite, Observed, Indirect),
				row("c2", SelfBlame, Observed, Indirect),
				row("c2", Concentration, Observed, Indirect),
				row("c2", Psychomotor, Observed, Indirect),
				row("c2", Interest, NotObserved, Direct),
			},
			want: map[Item]Judgement{
				Interest: observedDirect, Mood: observedDirect, Sleep: observedDirect, Fatigue: observedDirect,
				Appetite: observedIndirect, SelfBlame: observedIndirect,
				Concentration: observedIndirect, Psychomotor: observedIndirect,
			},
			wantObserved: 8,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := slices.Clone(tt.rows)

			day, err := MergeDay(date, tt.rows)

			require.NoError(t, err)
			assert.Equal(t, dayWith(date, tt.want), day)
			assert.Equal(t, tt.wantObserved, day.ObservedCount())
			require.NoError(t, day.Validate(), "합친 결과는 언제나 판단과 명시성이 맞는다")
			assert.Equal(t, before, tt.rows, "받은 행을 고치지 않는다")
		})
	}
}

func TestMergeDayIsOrderIndependent(t *testing.T) {
	date := mustDate(t, "2026-10-03")
	rows := []Row{
		row("c1", Sleep, NotObserved, Direct),
		row("c2", Sleep, Observed, Indirect),
		cancelled(row("c3", Sleep, Observed, Direct)),
		row("c4", Sleep, NotMentioned, None),
		row("c2", Mood, NotObserved, Indirect),
	}
	want := dayWith(date, map[Item]Judgement{
		Sleep: {Status: Observed, Explicitness: Indirect},
		Mood:  {Status: NotObserved, Explicitness: Indirect},
	})

	t.Run("행을 어떤 순서로 넘겨도 결과가 같다", func(t *testing.T) {
		count := 0
		permute(rows, func(shuffled []Row) {
			count++
			day, err := MergeDay(date, shuffled)

			require.NoError(t, err)
			require.Equal(t, want, day)
		})
		assert.Equal(t, 120, count, "다섯 행의 모든 순서를 다 돌아야 한다")
	})
}

// permute는 rows의 모든 순서를 하나씩 visit에 넘긴다. 난수를 쓰지 않고 전부 돌아서 시험이 실행마다 같다.
func permute(rows []Row, visit func([]Row)) {
	var walk func(current []Row, rest []Row)
	walk = func(current []Row, rest []Row) {
		if len(rest) == 0 {
			visit(current)
			return
		}
		for i := range rest {
			next := append(slices.Clone(current), rest[i])
			remaining := append(slices.Clone(rest[:i]), rest[i+1:]...)
			walk(next, remaining)
		}
	}
	walk(nil, rows)
}

func TestMergeDayErrors(t *testing.T) {
	date := mustDate(t, "2026-10-03")
	valid := row("c0", Mood, Observed, Direct)

	tests := []struct {
		name    string
		date    recorddate.Date
		rows    []Row
		wantErr error
		// wantRow가 0 이상이면 그 자리의 행이 문제라고 알려줘야 한다.
		wantRow int
	}{
		{"날짜가 빈 값이다", recorddate.Date{}, []Row{valid}, ErrZeroDate, -1},
		{"행이 nil이다", date, nil, ErrNoRows, -1},
		{"행이 하나도 없다", date, []Row{}, ErrNoRows, -1},
		{"항목을 채우지 않은 행", date, []Row{{ConversationID: "c1", Status: Observed, Explicitness: Direct}}, ErrInvalidItem, 0},
		{"항목이 아닌 값", date, []Row{valid, row("c1", Psychomotor+1, Observed, Direct)}, ErrInvalidItem, 1},
		{"판단이 정해진 값이 아니다", date, []Row{valid, valid, row("c1", Sleep, Observed+1, Direct)}, ErrInvalidStatus, 2},
		{"명시성이 정해진 값이 아니다", date, []Row{row("c1", Sleep, Observed, Direct+1)}, ErrInvalidExplicitness, 0},
		{"관찰됨인데 근거가 없다", date, []Row{row("c1", Sleep, Observed, None)}, ErrInconsistentJudgement, 0},
		{"관찰되지 않음인데 근거가 없다", date, []Row{row("c1", Sleep, NotObserved, None)}, ErrInconsistentJudgement, 0},
		{"언급 없음인데 직접 언급이다", date, []Row{row("c1", Sleep, NotMentioned, Direct)}, ErrInconsistentJudgement, 0},
		{"취소된 행이어도 틀린 값은 봐주지 않는다", date, []Row{cancelled(row("c1", Sleep, Observed, None))}, ErrInconsistentJudgement, 0},
	}
	for _, tt := range tests {
		t.Run("거부: "+tt.name, func(t *testing.T) {
			day, err := MergeDay(tt.date, tt.rows)

			require.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, Day{}, day, "실패하면 빈 값을 돌려준다")

			var dayErr *DayError
			require.ErrorAs(t, err, &dayErr)
			assert.Equal(t, tt.date, dayErr.Date)
			assert.Equal(t, -1, dayErr.Index, "슬라이스로 받은 하루가 아니다")

			var rowErr *RowError
			if tt.wantRow < 0 {
				assert.NotErrorAs(t, err, &rowErr)
				return
			}
			require.ErrorAs(t, err, &rowErr)
			assert.Equal(t, tt.wantRow, rowErr.Index)
			assert.Equal(t, tt.rows[tt.wantRow].ConversationID, rowErr.ConversationID)
		})
	}

	t.Run("오류 메시지에 대화 식별자를 담지 않는다", func(t *testing.T) {
		_, err := MergeDay(date, []Row{row("conversation-7f3a", Sleep, Observed, None)})

		require.Error(t, err)
		assert.NotContains(t, err.Error(), "conversation-7f3a")
		assert.Contains(t, err.Error(), "2026-10-03")
	})
}

func TestDayJudgement(t *testing.T) {
	date := mustDate(t, "2026-10-03")
	day := dayWith(date, map[Item]Judgement{
		Sleep: {Status: Observed, Explicitness: Direct},
		Mood:  {Status: NotObserved, Explicitness: Indirect},
	})

	t.Run("항목별 판단을 돌려준다", func(t *testing.T) {
		assert.Equal(t, Judgement{Status: Observed, Explicitness: Direct}, day.Judgement(Sleep))
		assert.Equal(t, Judgement{Status: NotObserved, Explicitness: Indirect}, day.Judgement(Mood))
		assert.Equal(t, Judgement{}, day.Judgement(Appetite))
	})

	t.Run("항목이 아닌 값을 넘기면 언급 없음이다", func(t *testing.T) {
		assert.Equal(t, Judgement{}, day.Judgement(0))
		assert.Equal(t, Judgement{}, day.Judgement(Psychomotor+1))
	})

	t.Run("빈 하루는 여덟 항목이 모두 언급 없음이다", func(t *testing.T) {
		empty := Day{Date: date}

		for _, item := range AllItems() {
			assert.Equal(t, Judgement{Status: NotMentioned, Explicitness: None}, empty.Judgement(item))
		}
		assert.Zero(t, empty.ObservedCount())
		require.NoError(t, empty.Validate())
	})

	t.Run("관찰되지 않음은 관찰된 항목 수에 들지 않는다", func(t *testing.T) {
		assert.Equal(t, 1, day.ObservedCount())
	})
}

func TestDayValidate(t *testing.T) {
	date := mustDate(t, "2026-10-03")

	t.Run("날짜가 빠진 하루", func(t *testing.T) {
		err := Day{}.Validate()

		require.ErrorIs(t, err, ErrZeroDate)
	})

	t.Run("직접 채운 판단이 명시성과 맞지 않으면 어느 항목인지 알려준다", func(t *testing.T) {
		day := dayWith(date, map[Item]Judgement{SelfBlame: {Status: Observed, Explicitness: None}})

		err := day.Validate()

		require.ErrorIs(t, err, ErrInconsistentJudgement)
		assert.Contains(t, err.Error(), "self_blame")
		var dayErr *DayError
		require.ErrorAs(t, err, &dayErr)
		assert.Equal(t, date, dayErr.Date)
	})
}

func TestMergeDays(t *testing.T) {
	first := mustDate(t, "2026-10-01")
	second := mustDate(t, "2026-10-02")
	third := mustDate(t, "2026-10-05")

	t.Run("날짜별 행을 하루씩 합쳐 날짜순으로 돌려준다", func(t *testing.T) {
		days, err := MergeDays(map[recorddate.Date][]Row{
			third:  {row("c3", Fatigue, Observed, Indirect)},
			first:  {row("c1", Sleep, Observed, Direct), row("c1", Mood, NotObserved, Direct)},
			second: {cancelled(row("c2", Sleep, Observed, Direct))},
		})

		require.NoError(t, err)
		require.NoError(t, ValidateDays(days))
		assert.Equal(t, []Day{
			dayWith(first, map[Item]Judgement{
				Sleep: {Status: Observed, Explicitness: Direct},
				Mood:  {Status: NotObserved, Explicitness: Direct},
			}),
			dayWith(second, nil),
			dayWith(third, map[Item]Judgement{Fatigue: {Status: Observed, Explicitness: Indirect}}),
		}, days)
	})

	t.Run("신호 행이 없는 날은 대화하지 않은 날처럼 빠진다", func(t *testing.T) {
		days, err := MergeDays(map[recorddate.Date][]Row{
			first:  {row("c1", Sleep, Observed, Direct)},
			second: {},
			third:  nil,
		})

		require.NoError(t, err)
		require.Len(t, days, 1)
		assert.Equal(t, first, days[0].Date)
	})

	t.Run("입력이 비어 있으면 빈 목록이다", func(t *testing.T) {
		days, err := MergeDays(nil)

		require.NoError(t, err)
		assert.Empty(t, days)
	})

	t.Run("틀린 날이 여럿이면 언제나 가장 이른 날짜를 알려준다", func(t *testing.T) {
		rowsByDate := map[recorddate.Date][]Row{
			first:  {row("c1", Sleep, Observed, Direct)},
			second: {row("c2", Sleep, Observed, None)},
			third:  {row("c3", 0, Observed, Direct)},
		}

		// 맵을 도는 순서는 실행마다 다르다. 여러 번 돌려도 같은 오류여야 한다.
		for range 20 {
			days, err := MergeDays(rowsByDate)

			require.ErrorIs(t, err, ErrInconsistentJudgement)
			assert.Nil(t, days)
			var dayErr *DayError
			require.ErrorAs(t, err, &dayErr)
			assert.Equal(t, second, dayErr.Date)
		}
	})

	t.Run("날짜가 빈 값인데 행이 있으면 거부한다", func(t *testing.T) {
		_, err := MergeDays(map[recorddate.Date][]Row{
			{}: {row("c1", Sleep, Observed, Direct)},
		})

		require.ErrorIs(t, err, ErrZeroDate)
	})
}

func TestSortDays(t *testing.T) {
	d1 := mustDate(t, "2026-09-30")
	d2 := mustDate(t, "2026-10-01")
	d3 := mustDate(t, "2026-10-04")
	sleepObserved := map[Item]Judgement{Sleep: {Status: Observed, Explicitness: Direct}}

	t.Run("날짜순으로 늘어놓은 복사본을 돌려주고 받은 것은 고치지 않는다", func(t *testing.T) {
		input := []Day{dayWith(d3, nil), dayWith(d1, sleepObserved), dayWith(d2, nil)}
		before := slices.Clone(input)

		sorted, err := SortDays(input)

		require.NoError(t, err)
		assert.Equal(t, []Day{dayWith(d1, sleepObserved), dayWith(d2, nil), dayWith(d3, nil)}, sorted)
		assert.Equal(t, before, input)
		require.NoError(t, ValidateDays(sorted))
	})

	t.Run("돌려준 복사본을 고쳐도 받은 것은 그대로다", func(t *testing.T) {
		input := []Day{dayWith(d1, nil)}

		sorted, err := SortDays(input)
		require.NoError(t, err)
		sorted[0].Judgements[Sleep.Index()] = Judgement{Status: Observed, Explicitness: Direct}

		assert.Equal(t, Judgement{}, input[0].Judgement(Sleep))
	})

	t.Run("비어 있으면 빈 목록이다", func(t *testing.T) {
		sorted, err := SortDays(nil)

		require.NoError(t, err)
		assert.Empty(t, sorted)
	})

	invalid := []struct {
		name      string
		days      []Day
		wantErr   error
		wantIndex int
		wantDate  recorddate.Date
	}{
		{
			name:      "같은 날짜가 두 번 나온다",
			days:      []Day{dayWith(d2, nil), dayWith(d1, nil), dayWith(d2, sleepObserved)},
			wantErr:   ErrDuplicateDate,
			wantIndex: 2,
			wantDate:  d2,
		},
		{
			name:      "날짜가 빠진 하루가 섞여 있다",
			days:      []Day{dayWith(d1, nil), {}},
			wantErr:   ErrZeroDate,
			wantIndex: 1,
			wantDate:  recorddate.Date{},
		},
		{
			name:      "판단과 명시성이 맞지 않는 하루가 섞여 있다",
			days:      []Day{dayWith(d3, map[Item]Judgement{Mood: {Status: NotMentioned, Explicitness: Direct}}), dayWith(d1, nil)},
			wantErr:   ErrInconsistentJudgement,
			wantIndex: 0,
			wantDate:  d3,
		},
	}
	for _, tt := range invalid {
		t.Run("거부: "+tt.name, func(t *testing.T) {
			sorted, err := SortDays(tt.days)

			require.ErrorIs(t, err, tt.wantErr)
			assert.Nil(t, sorted)
			var dayErr *DayError
			require.ErrorAs(t, err, &dayErr)
			assert.Equal(t, tt.wantIndex, dayErr.Index, "받은 슬라이스에서의 자리를 가리킨다")
			assert.Equal(t, tt.wantDate, dayErr.Date)
		})
	}
}

func TestValidateDays(t *testing.T) {
	d1 := mustDate(t, "2026-09-30")
	d2 := mustDate(t, "2026-10-01")
	d3 := mustDate(t, "2026-10-04")

	valid := []struct {
		name string
		days []Day
	}{
		{"nil", nil},
		{"하루", []Day{dayWith(d1, nil)}},
		{"달을 넘겨 이어지는 날짜", []Day{dayWith(d1, nil), dayWith(d2, nil)}},
		{"대화하지 않은 날이 사이에 끼어 있어도 된다", []Day{dayWith(d1, nil), dayWith(d2, nil), dayWith(d3, nil)}},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, ValidateDays(tt.days))
		})
	}

	invalid := []struct {
		name      string
		days      []Day
		wantErr   error
		wantIndex int
	}{
		{"같은 날짜가 이어서 나온다", []Day{dayWith(d1, nil), dayWith(d2, nil), dayWith(d2, nil)}, ErrDuplicateDate, 2},
		{"날짜가 거꾸로다", []Day{dayWith(d2, nil), dayWith(d1, nil)}, ErrUnsortedDays, 1},
		{"중간에 앞선 날짜가 끼어 있다", []Day{dayWith(d1, nil), dayWith(d3, nil), dayWith(d2, nil)}, ErrUnsortedDays, 2},
		{"날짜가 빠진 하루", []Day{{}, dayWith(d1, nil)}, ErrZeroDate, 0},
		{
			"판단과 명시성이 맞지 않는 하루",
			[]Day{dayWith(d1, nil), dayWith(d2, map[Item]Judgement{Sleep: {Status: Observed, Explicitness: None}})},
			ErrInconsistentJudgement, 1,
		},
	}
	for _, tt := range invalid {
		t.Run("거부: "+tt.name, func(t *testing.T) {
			err := ValidateDays(tt.days)

			require.ErrorIs(t, err, tt.wantErr)
			var dayErr *DayError
			require.ErrorAs(t, err, &dayErr)
			assert.Equal(t, tt.wantIndex, dayErr.Index)
			assert.Equal(t, tt.days[tt.wantIndex].Date, dayErr.Date)
		})
	}

	t.Run("메시지에서 몇 번째 하루의 어느 날짜인지 읽을 수 있다", func(t *testing.T) {
		err := ValidateDays([]Day{dayWith(d2, nil), dayWith(d1, nil)})

		require.EqualError(t, err, "signal day 1 (2026-09-30): signal: days are not in ascending date order")
	})
}
