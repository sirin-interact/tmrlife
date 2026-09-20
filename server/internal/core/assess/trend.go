package assess

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/sirin-interact/tmrlife/server/internal/core/baseline"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/score"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// 식별자를 읽지 못했을 때의 오류다. 메시지에는 입력받은 글자를 담지 않는다.
var (
	ErrInvalidMark       = errors.New("assess: invalid mark")
	ErrInvalidComparison = errors.New("assess: invalid comparison")
)

// Mark는 점 달력의 한 칸에 그리는 표시다.
//
// 신호는 하루 단위의 관찰됨, 관찰되지 않음, 언급 없음뿐이라 세로축을 가진 그래프로 그릴 값이 없다.
// 그래서 추세 화면은 날짜마다 점 하나를 찍는 달력이다.
// 빈 값은 대화하지 않은 날이다. 채우지 않은 칸은 빈칸으로 그려진다.
type Mark int

const (
	// MarkNoConversation은 대화하지 않은 날이다. 빈칸으로 둔다.
	MarkNoConversation Mark = iota
	// MarkNotMentioned는 대화는 했지만 그 줄의 이야기가 나오지 않은 날이다. 작은 점이다.
	MarkNotMentioned
	// MarkNotObserved는 그 줄의 이야기가 나왔고 괜찮았던 날이다. 빈 점이다.
	MarkNotObserved
	// MarkObserved는 그 줄의 신호가 관찰된 날이다. 찬 점이다.
	MarkObserved
)

// ParseMark는 식별자(no_conversation, not_mentioned, not_observed, observed)를 읽는다.
func ParseMark(id string) (Mark, error) {
	for m := MarkNoConversation; m <= MarkObserved; m++ {
		if m.String() == id {
			return m, nil
		}
	}
	return MarkNoConversation, fmt.Errorf("%w: unknown id", ErrInvalidMark)
}

// Valid는 네 표시 가운데 하나인지 알려준다.
func (m Mark) Valid() bool {
	return m >= MarkNoConversation && m <= MarkObserved
}

// String은 표시의 식별자다. 대화한 날의 세 식별자는 판단의 식별자와 같다. 정해진 값이 아니면 Mark(n) 꼴로 적는다.
func (m Mark) String() string {
	switch m {
	case MarkNoConversation:
		return "no_conversation"
	case MarkNotMentioned:
		return signal.NotMentioned.String()
	case MarkNotObserved:
		return signal.NotObserved.String()
	case MarkObserved:
		return signal.Observed.String()
	default:
		return "Mark(" + strconv.Itoa(int(m)) + ")"
	}
}

// MarshalText는 JSON에 식별자로 적히게 한다. 정해진 값이 아니면 오류다.
func (m Mark) MarshalText() ([]byte, error) {
	if !m.Valid() {
		return nil, fmt.Errorf("%w: cannot be encoded", ErrInvalidMark)
	}
	return []byte(m.String()), nil
}

// UnmarshalText는 ParseMark와 같은 규칙으로 읽는다. 실패하면 값을 바꾸지 않는다.
func (m *Mark) UnmarshalText(text []byte) error {
	parsed, err := ParseMark(string(text))
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// markOf는 대화한 날의 판단을 표시로 옮긴다.
func markOf(status signal.Status) Mark {
	switch status {
	case signal.Observed:
		return MarkObserved
	case signal.NotObserved:
		return MarkNotObserved
	default:
		return MarkNotMentioned
	}
}

// Comparison은 최근 기간의 빈도를 평소의 빈도와 견준 결과다.
//
// 빈 값은 "견줄 수 없음"이다. 평소가 아직 없거나 최근의 기록이 모자랄 때다.
// 채우지 않은 비교가 "평소와 비슷함"으로 읽히는 일이 없다.
type Comparison int

const (
	// ComparisonNone은 견줄 수 없다는 뜻이다. 화면에는 일수만 보여주고 평소와 견주는 말은 붙이지 않는다.
	ComparisonNone Comparison = iota
	// LessOften은 평소보다 드물다는 뜻이다. 나아진 것도 되짚어 줄 수 있게 따로 둔다.
	LessOften
	// Similar는 평소와 비슷하다는 뜻이다.
	Similar
	// MoreOften은 평소보다 잦다는 뜻이다.
	MoreOften
)

// ParseComparison은 식별자(none, less_often, similar, more_often)를 읽는다.
func ParseComparison(id string) (Comparison, error) {
	for c := ComparisonNone; c <= MoreOften; c++ {
		if c.String() == id {
			return c, nil
		}
	}
	return ComparisonNone, fmt.Errorf("%w: unknown id", ErrInvalidComparison)
}

// Valid는 정해진 네 값(견줄 수 없음 포함) 가운데 하나인지 알려준다.
func (c Comparison) Valid() bool {
	return c >= ComparisonNone && c <= MoreOften
}

// String은 비교 결과의 식별자다. 정해진 값이 아니면 Comparison(n) 꼴로 적는다.
func (c Comparison) String() string {
	switch c {
	case ComparisonNone:
		return "none"
	case LessOften:
		return "less_often"
	case Similar:
		return "similar"
	case MoreOften:
		return "more_often"
	default:
		return "Comparison(" + strconv.Itoa(int(c)) + ")"
	}
}

// MarshalText는 JSON에 식별자로 적히게 한다. 정해진 값이 아니면 오류다.
func (c Comparison) MarshalText() ([]byte, error) {
	if !c.Valid() {
		return nil, fmt.Errorf("%w: cannot be encoded", ErrInvalidComparison)
	}
	return []byte(c.String()), nil
}

// UnmarshalText는 ParseComparison과 같은 규칙으로 읽는다. 실패하면 값을 바꾸지 않는다.
func (c *Comparison) UnmarshalText(text []byte) error {
	parsed, err := ParseComparison(string(text))
	if err != nil {
		return err
	}
	*c = parsed
	return nil
}

// RowTrend는 점 달력의 한 줄이다. "11일 중 7일, 평소보다 잦음"이라는 말을 만드는 데 필요한 값을 모두 담는다.
type RowTrend struct {
	Row signal.TrendRow
	// Marks는 Trend.Dates와 같은 순서, 같은 길이로 날짜마다 표시 하나씩이다.
	Marks []Mark
	// ObservedDays는 창 안에서 이 줄의 신호가 관찰된 일수다.
	ObservedDays int
	// ConversationDays는 창 안에서 대화한 일수다. Trend.ConversationDays와 같다.
	ConversationDays int
	// Usual은 기준선 기간에 이 줄의 신호가 관찰된 빈도다. 기준선이 잡히기 전에는 빈 값이다.
	// 모으는 중인 값은 아직 평소가 아니므로 내보내지 않는다.
	Usual baseline.Rate
	// Comparison은 최근의 빈도(ObservedDays ÷ ConversationDays)를 Usual과 견준 결과다.
	Comparison Comparison
}

// Trend는 추세 화면의 점 달력이다. 줄은 기분, 수면, 에너지 셋이고 칸은 창 안의 날짜마다 하나다.
//
// 일수는 보여주되 추정 점수와 단계는 여기에 담지 않는다. 사용자 화면에는 그 숫자들을 내보내지 않는다.
type Trend struct {
	// From과 To는 달력의 첫날과 마지막 날이다. 점수가 보는 창과 같고 To가 기준일이다.
	From recorddate.Date
	To   recorddate.Date
	// Dates는 From부터 To까지의 날짜다. 대화하지 않은 날도 들어 있다.
	Dates []recorddate.Date
	// ConversationDays는 창 안에서 대화한 일수다.
	ConversationDays int
	// Rows의 자리는 signal.TrendRow.Index다. 화면에 그리는 순서와 같다.
	Rows [signal.TrendRowCount]RowTrend
}

// Row는 그 줄의 값을 돌려준다. 줄이 아닌 값을 넘기면 빈 값을 돌려준다.
func (t Trend) Row(row signal.TrendRow) RowTrend {
	idx := row.Index()
	if idx < 0 {
		return RowTrend{}
	}
	return t.Rows[idx]
}

// buildTrend는 창 안의 날짜마다 세 줄의 표시를 채우고 평소와 견준다. days는 날짜순이어야 한다.
func buildTrend(days []signal.Day, window score.Window, base baseline.Baseline, p params.Params) Trend {
	length := window.Length()
	trend := Trend{From: window.From, To: window.To, Dates: make([]recorddate.Date, 0, length)}
	for offset := range length {
		trend.Dates = append(trend.Dates, window.From.AddDays(offset))
	}
	for _, row := range signal.AllTrendRows() {
		// 빈 값이 대화하지 않은 날이라, 아래에서 채우지 않은 칸은 저절로 빈칸이 된다.
		trend.Rows[row.Index()] = RowTrend{Row: row, Marks: make([]Mark, length)}
	}

	inside := window.DaysIn(days)
	trend.ConversationDays = len(inside)
	for _, day := range inside {
		offset := day.Date.DaysSince(window.From)
		for _, row := range signal.AllTrendRows() {
			status := day.TrendStatus(row)
			line := &trend.Rows[row.Index()]
			line.Marks[offset] = markOf(status)
			if status == signal.Observed {
				line.ObservedDays++
			}
		}
	}

	for idx := range trend.Rows {
		line := &trend.Rows[idx]
		line.ConversationDays = trend.ConversationDays
		if base.Established {
			line.Usual = base.TrendRate(line.Row)
		}
		line.Comparison = compare(line.ObservedDays, line.ConversationDays, line.Usual, p)
	}
	return trend
}

// compare는 최근의 빈도(observed ÷ conversationDays)를 평소의 빈도와 견준다.
//
// 두 비율의 차이가 p.Trend.MinDifferencePercent 퍼센트포인트 이상이면 잦음이나 드묾이고, 그보다 작으면 비슷함이다.
// 하루의 차이는 흔한 기복이라 그것만으로 평소와 다르다고 말하지 않는다.
//
// 견줄 수 없는 경우가 둘 있다. 평소가 아직 없을 때(usual이 빈 값), 그리고 창 안에서 대화한 날이
// 기록 부족의 기준에 못 미칠 때다. 사흘 치 기록의 "세 번 중 두 번"을 두 주의 빈도처럼 말하지 않는다.
//
// 나눗셈을 하지 않는다. o/n − uo/un ≥ m/100 의 양변에 100·n·un을 곱해 정수끼리 견준다.
// 차이가 기준과 딱 같은 경우(10일 중 4일과 10일 중 2일, 기준 20)가 소수 오차로 흔들리지 않는다.
func compare(observed, conversationDays int, usual baseline.Rate, p params.Params) Comparison {
	if usual.Days <= 0 || conversationDays < p.Window.MinConversationDays {
		return ComparisonNone
	}
	difference := 100 * (observed*usual.Days - usual.ObservedDays*conversationDays)
	margin := p.Trend.MinDifferencePercent * conversationDays * usual.Days
	switch {
	case difference >= margin:
		return MoreOften
	case -difference >= margin:
		return LessOften
	default:
		return Similar
	}
}
