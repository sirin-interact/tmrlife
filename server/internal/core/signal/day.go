package signal

import (
	"fmt"
	"slices"

	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// Row는 대화 하나에서 항목 하나에 대해 나온 판단이다.
type Row struct {
	// ConversationID는 그 판단이 나온 대화다. 계산에는 쓰지 않고 어디서 온 행인지 가리키는 데만 쓴다.
	// 형식을 따지지 않는다.
	ConversationID string
	Item           Item
	Status         Status
	Explicitness   Explicitness
	// Cancelled는 사용자가 "이건 아니에요"로 취소한 신호라는 뜻이다. 행은 남아 있지만 계산에서는 뺀다.
	Cancelled bool
}

// Judgement는 행의 판단과 명시성을 묶어 돌려준다.
func (r Row) Judgement() Judgement {
	return Judgement{Status: r.Status, Explicitness: r.Explicitness}
}

// Validate는 항목이 여덟 항목 가운데 하나인지, 판단과 명시성이 서로 맞는지 본다.
// 취소된 행에도 같은 규칙을 적용한다. 취소는 계산에서 빼라는 뜻이지 틀린 값을 봐주라는 뜻이 아니다.
func (r Row) Validate() error {
	if !r.Item.Valid() {
		return ErrInvalidItem
	}
	return r.Judgement().Validate()
}

// Day는 기록 날짜 하루의 판단을 항목별로 하나씩 합친 것이다.
//
// Day가 있다는 것 자체가 "그날 대화했고 분석이 끝났다"는 뜻이다.
// 대화하지 않은 날, 분석을 꺼 둔 날은 Day를 만들지 않는다. 그래야 그날이 대화한 일수에 들지 않는다.
type Day struct {
	Date recorddate.Date
	// Judgements의 자리는 Item.Index다. 빈 값은 여덟 항목 모두 "언급 없음"이다.
	// 직접 채울 때는 Validate로 판단과 명시성이 맞는지 확인한다.
	Judgements [ItemCount]Judgement
}

// Judgement는 그날 그 항목의 판단이다. 항목이 아닌 값을 넘기면 "언급 없음"을 돌려준다.
func (d Day) Judgement(item Item) Judgement {
	idx := item.Index()
	if idx < 0 {
		return Judgement{}
	}
	return d.Judgements[idx]
}

// ObservedCount는 그날 관찰된 항목의 수(0부터 8)다.
// 개인 기준선의 하루 평균과 변화 탐지가 이 값을 날마다 본다.
func (d Day) ObservedCount() int {
	count := 0
	for _, j := range d.Judgements {
		if j.Status == Observed {
			count++
		}
	}
	return count
}

// Validate는 날짜가 채워져 있는지, 항목마다 판단과 명시성이 맞는지 본다.
// 오류는 *DayError다.
func (d Day) Validate() error {
	if err := d.check(); err != nil {
		return &DayError{Index: -1, Date: d.Date, Err: err}
	}
	return nil
}

func (d Day) check() error {
	if d.Date.IsZero() {
		return ErrZeroDate
	}
	for _, item := range AllItems() {
		if err := d.Judgements[item.Index()].Validate(); err != nil {
			return fmt.Errorf("%s: %w", item, err)
		}
	}
	return nil
}

// MergeDay는 기록 날짜 하루에 속한 신호 행들을 항목별 판단 하나씩으로 합친다.
//
//   - 취소된 행은 뺀다. 빠진 자리는 언급 없음과 같다.
//   - 그날 한 번이라도 관찰되면 관찰됨이다. 우선순위는 관찰됨, 관찰되지 않음, 언급 없음 순이다.
//     아침 대화에서 "잘 잤어"라고 했어도 밤 대화에서 잠을 못 잤다는 이야기가 나왔으면 그날은 관찰됨이다.
//   - 명시성은 그날의 판단으로 뽑힌 쪽의 행만 본다. 그중 하나라도 직접 언급이면 직접 언급이다.
//     관찰됨이 간접 추론이고 관찰되지 않음이 직접 언급이면, 그날은 "관찰됨, 간접 추론"이다.
//   - 어떤 행에도 나오지 않은 항목은 언급 없음이다.
//
// 행의 순서는 결과에 영향을 주지 않는다. rows는 고치지 않는다.
//
// 행이 하나도 없으면 ErrNoRows다. 신호 행이 없는 날(분석을 꺼 둔 날)은 대화하지 않은 날과 똑같이
// 입력에서 빠져야 하는데, 빈 하루를 만들어 주면 그날이 대화한 일수에 들어가 점수와 신뢰도가 조용히 낮아진다.
// 행이 모두 취소된 날은 다르다. 대화와 분석이 있었으므로 여덟 항목이 모두 언급 없음인 하루가 된다.
//
// 오류는 *DayError이고, 행이 틀린 경우에는 그 안에 *RowError가 들어 있다.
func MergeDay(date recorddate.Date, rows []Row) (Day, error) {
	if date.IsZero() {
		return Day{}, &DayError{Index: -1, Date: date, Err: ErrZeroDate}
	}
	if len(rows) == 0 {
		return Day{}, &DayError{Index: -1, Date: date, Err: ErrNoRows}
	}

	day := Day{Date: date}
	for i, row := range rows {
		if err := row.Validate(); err != nil {
			rowErr := &RowError{Index: i, ConversationID: row.ConversationID, Err: err}
			return Day{}, &DayError{Index: -1, Date: date, Err: rowErr}
		}
		if row.Cancelled {
			continue
		}
		idx := row.Item.Index()
		day.Judgements[idx] = stronger(day.Judgements[idx], row.Judgement())
	}
	return day, nil
}

// stronger는 두 판단 가운데 하루의 판단으로 남을 쪽을 고른다.
// 판단이 다르면 우선순위가 높은 쪽이고, 같으면 근거가 더 분명한 쪽이다.
// 어느 쪽을 먼저 넘겨도 결과가 같아서 행의 순서가 결과를 바꾸지 못한다.
func stronger(a, b Judgement) Judgement {
	if a.Status != b.Status {
		if b.Status > a.Status {
			return b
		}
		return a
	}
	if b.Explicitness > a.Explicitness {
		return b
	}
	return a
}

// MergeDays는 날짜별로 모은 신호 행을 하루씩 합쳐 날짜순으로 돌려준다.
//
// 행이 하나도 없는 날짜는 건너뛴다. 분석을 꺼 둔 날에는 신호 행이 생기지 않는데,
// 그런 날은 대화하지 않은 날과 똑같이 대화한 일수에서 빠져야 한다.
// 오류가 여러 날에 있으면 가장 이른 날짜의 것을 돌려준다. 맵을 도는 순서에 따라 결과가 달라지지 않는다.
//
// # 행을 넘기는 쪽이 지킬 것
//
// 그날이 대화한 날인지는 행이 있는지로만 안다. 그래서 행을 만드는 쪽과 읽어 오는 쪽이 아래를 지켜야 한다.
//
//   - 분석이 끝난 대화는 여덟 항목 모두에 대해 행을 남긴다. 아무 항목도 나오지 않았으면 여덟 행이 모두 언급 없음이다.
//     언급된 항목의 행만 남기면, 별말 없이 지나간 날이 대화한 일수에서 통째로 빠진다.
//     그러면 나눌 일수가 줄어 점수가 실제보다 높게 나오고, 기록 충실도는 낮아지고, 평소의 하루 평균도 높게 잡힌다.
//   - 분석이 아직 끝나지 않았거나 실패한 대화의 행은 넘기지 않는다. 일부 항목만 본 결과가 하루의 판단으로 굳지 않게 한다.
//     그런 대화뿐인 날은 분석이 끝날 때까지 대화하지 않은 날과 같다.
func MergeDays(rowsByDate map[recorddate.Date][]Row) ([]Day, error) {
	dates := make([]recorddate.Date, 0, len(rowsByDate))
	for date, rows := range rowsByDate {
		if len(rows) > 0 {
			dates = append(dates, date)
		}
	}
	slices.SortFunc(dates, recorddate.Date.Compare)

	days := make([]Day, 0, len(dates))
	for _, date := range dates {
		day, err := MergeDay(date, rowsByDate[date])
		if err != nil {
			return nil, err
		}
		days = append(days, day)
	}
	return days, nil
}

// SortDays는 날짜순으로 늘어놓은 복사본을 돌려준다. 받은 슬라이스는 고치지 않는다.
//
// 날짜가 빠진 하루, 판단과 명시성이 맞지 않는 하루, 같은 날짜가 두 번 나오는 경우는 오류(*DayError)다.
// 같은 날짜가 둘이면 어느 쪽이 맞는지 여기서 고를 수 없다. 합치는 일은 MergeDay가 행을 보고 해야 한다.
func SortDays(days []Day) ([]Day, error) {
	for i, day := range days {
		if err := day.check(); err != nil {
			return nil, &DayError{Index: i, Date: day.Date, Err: err}
		}
	}

	// 오류가 입력에서의 자리를 가리키도록 자리 번호를 정렬한다.
	order := make([]int, len(days))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return days[a].Date.Compare(days[b].Date)
	})

	// 날짜가 빈 하루는 위에서 걸러졌으므로 빈 값으로 시작해도 첫 하루와 겹치지 않는다.
	var previous recorddate.Date
	sorted := make([]Day, 0, len(days))
	for _, idx := range order {
		day := days[idx]
		if day.Date == previous {
			return nil, &DayError{Index: idx, Date: day.Date, Err: ErrDuplicateDate}
		}
		previous = day.Date
		sorted = append(sorted, day)
	}
	return sorted, nil
}

// ValidateDays는 하루의 목록이 계산에 넣어도 되는 꼴인지 본다.
// 날짜가 앞에서부터 커지기만 해야 하고(같은 날짜도 안 된다), 하루하루가 Validate를 통과해야 한다.
//
// 창을 자르고 첫날부터 다시 돌리는 계산은 모두 이 순서에 기대고 있다.
// 정렬되지 않은 입력을 조용히 받아 주면 틀린 값이 나와도 알아채기 어렵다.
// 오류는 *DayError이고 Index는 문제가 드러난 자리다.
func ValidateDays(days []Day) error {
	// 빈 날짜는 모든 날짜보다 앞서고, 빈 날짜를 가진 하루는 check에서 걸러진다.
	// 그래서 첫 하루를 따로 다루지 않아도 된다.
	var previous recorddate.Date
	for i, day := range days {
		if err := day.check(); err != nil {
			return &DayError{Index: i, Date: day.Date, Err: err}
		}
		switch day.Date.Compare(previous) {
		case 0:
			return &DayError{Index: i, Date: day.Date, Err: ErrDuplicateDate}
		case -1:
			return &DayError{Index: i, Date: day.Date, Err: ErrUnsortedDays}
		}
		previous = day.Date
	}
	return nil
}
