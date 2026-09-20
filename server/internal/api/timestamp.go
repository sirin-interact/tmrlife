package api

import (
	"errors"
	"time"
)

// utcTimeLayout은 응답에 싣는 시각의 꼴이다. 자바스크립트의 Date.prototype.toISOString과 같다.
const utcTimeLayout = "2006-01-02T15:04:05.000Z"

// UTCTime은 응답에 싣는 시각이다. 어느 시간대의 값으로 만들었든 UTC로, 밀리초 세 자리까지 고정된 꼴로 나간다.
//
// time.Time을 그대로 실으면 꼴이 값에 따라 달라진다. 시간대가 다르면 끝이 Z가 아니라 +09:00이 되고,
// 소수점 아래 자릿수는 0으로 끝나는 만큼 줄어든다. 받는 쪽이 문자열로 견주거나 정렬해도 어긋나지 않게 꼴을 하나로 고정한다.
type UTCTime struct {
	t time.Time
}

// NewUTCTime은 t를 응답에 실을 시각으로 바꾼다. 밀리초 아래는 버린다.
func NewUTCTime(t time.Time) UTCTime {
	return UTCTime{t: t.UTC().Truncate(time.Millisecond)}
}

// Time은 담고 있는 시각을 돌려준다.
func (u UTCTime) Time() time.Time {
	return u.t
}

func (u UTCTime) MarshalJSON() ([]byte, error) {
	t := u.t.UTC()
	// RFC 3339는 네 자리 연도만 적을 수 있다.
	if y := t.Year(); y < 0 || y > 9999 {
		return nil, errors.New("api: year of timestamp is outside of 0..9999")
	}
	return []byte(`"` + t.Format(utcTimeLayout) + `"`), nil
}

func (u *UTCTime) UnmarshalJSON(data []byte) error {
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return errors.New("api: timestamp must be a JSON string")
	}
	t, err := time.Parse(time.RFC3339Nano, string(data[1:len(data)-1]))
	if err != nil {
		// time.Parse의 오류에는 받은 글자가 그대로 들어 있다. 옮겨 담지 않는다.
		return errors.New("api: timestamp is not in RFC 3339 format")
	}
	*u = NewUTCTime(t)
	return nil
}
