package rules

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// normalized는 표현을 찾기 좋게 다듬은 글이다. 다듬은 글의 바이트마다 원문의 어느 구간에서 왔는지를 함께 들고 있다.
//
// 사전은 다듬은 글에서 표현을 찾지만, 근거로 남기는 것은 사용자가 실제로 쓴 글이어야 한다.
// 다듬는 동안 글자가 빠지고 합쳐지므로 자리를 따로 적어 두지 않으면 원문으로 돌아갈 수 없다.
type normalized struct {
	text string
	// start[i]와 end[i]는 text의 i번째 바이트가 나온 원문 구간(바이트 자리, 끝은 포함하지 않음)이다.
	start []int
	end   []int
}

// normalize는 같은 말을 다르게 적은 것들을 한 가지 꼴로 모은다.
//
//   - 유니코드 호환 정규화(NFKC): 전각 글자, 조합형으로 들어온 한글, 호환용 글자를 보통 글자로 바꾼다.
//   - 글자와 숫자만 남긴다. 공백, 문장 부호, 기호, 이모지, 보이지 않는 글자, 낱자로 떨어진 자모(ㅋㅋ, ㅠㅠ)를 뺀다.
//     "죽고 싶다", "죽고싶다", "죽.고.싶.다", "죽고 싶다ㅠㅠ"가 모두 같은 글이 된다. 띄어쓰기는 사람마다 다르고,
//     채팅에서는 거의 지켜지지 않는다.
//   - 같은 글자가 세 번 이상 이어지면 두 번으로 줄인다. "죽고 싶다아아아아"와 "죽고 싶다아아"가 같아진다.
//     하나로 줄이지 않는 것은 "상상", "스스로", "했었었"처럼 같은 글자가 두 번 이어지는 멀쩡한 말이 있기 때문이다.
//     사전의 패턴에는 같은 글자를 세 번 연달아 적을 수 없다(적으면 영영 걸리지 않는다). 사전을 읽을 때 확인한다.
//   - 로마자는 소문자로 바꾼다.
//
// 들인 글의 길이에 비례하는 시간만 쓴다.
func normalize(s string) normalized {
	out := normalized{
		start: make([]int, 0, len(s)),
		end:   make([]int, 0, len(s)),
	}
	var b strings.Builder
	b.Grow(len(s))

	var (
		prev      rune = -1
		prevBytes int
		run       int
		it        norm.Iter
	)
	it.InitString(norm.NFKC, s)
	for !it.Done() {
		segStart := it.Pos()
		seg := it.Next()
		segEnd := it.Pos()

		for len(seg) > 0 {
			r, size := utf8.DecodeRune(seg)
			seg = seg[size:]
			if r == utf8.RuneError && size <= 1 {
				continue
			}
			r = unicode.ToLower(r)
			if !kept(r) {
				continue
			}
			if r == prev {
				run++
			} else {
				run = 1
			}
			if run > maxRun && squeezable(r) {
				// 줄여 없앤 글자도 근거에는 들어가야 한다. 남긴 글자의 끝을 여기까지 늘린다.
				for i := len(out.end) - prevBytes; i < len(out.end); i++ {
					out.end[i] = segEnd
				}
				continue
			}

			n, _ := b.WriteRune(r)
			for range n {
				out.start = append(out.start, segStart)
				out.end = append(out.end, segEnd)
			}
			prev, prevBytes = r, n
		}
	}

	out.text = b.String()
	return out
}

// maxRun은 같은 글자가 이어질 때 남기는 최대 개수다.
const maxRun = 2

// kept는 다듬은 글에 남길 글자인지 알려준다.
func kept(r rune) bool {
	if isJamo(r) {
		return false
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// squeezable은 길게 이어질 때 줄일 글자인지 알려준다. 숫자는 줄이지 않는다("1000"은 "100"이 아니다).
func squeezable(r rune) bool {
	return unicode.IsLetter(r)
}

// isJamo는 음절을 이루지 못하고 낱자로 남은 한글 자모인지 알려준다.
// 웃음과 울음을 적는 글자(ㅋㅋ, ㅎㅎ, ㅠㅠ)가 대부분이고, 표현 사이에 끼어 있어도 뜻을 바꾸지 않는다.
// 호환 자모(ㅋ)는 정규화를 거치면 첫가끝 자모로 바뀌므로 두 구역을 모두 본다.
func isJamo(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x11FF: // 첫가끝 자모
	case r >= 0x3130 && r <= 0x318F: // 호환 자모
	case r >= 0xA960 && r <= 0xA97F: // 첫가끝 자모 확장 A
	case r >= 0xD7B0 && r <= 0xD7FF: // 첫가끝 자모 확장 B
	case r >= 0xFFA0 && r <= 0xFFDC: // 반각 자모
	default:
		return false
	}
	return true
}

// span은 다듬은 글의 구간 [start, end)를 원문의 구간으로 옮긴다.
func (n normalized) span(start, end int) (int, int) {
	if start < 0 || end > len(n.text) || start >= end {
		return 0, 0
	}
	return n.start[start], n.end[end-1]
}
