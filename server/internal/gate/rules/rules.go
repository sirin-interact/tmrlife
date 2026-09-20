// Package rules는 위기 관문의 첫째 겹이다. 사용자의 발화에서 정해 둔 표현을 찾아 단계를 매긴다.
//
// 이 겹은 모델을 부르지 않는다. 그래서 언제나 같은 답을 즉시 내고, 모델이 멈춘 동안에도 돈다.
// 대신 앞뒤 대화를 읽지 못한다. 문맥으로만 풀리는 말은 둘째 겹(AI 판별)이 맡고,
// 두 겹의 판정은 core/crisis가 합친다. 합칠 때 높은 쪽을 따르므로 이 겹이 매긴 단계는 곧 바닥이다.
// 그래서 2단계와 3단계의 항목은 말한 사람 자신의, 지금의 이야기가 분명할 때만 걸리게 적는다.
// 넓게 잡는 일은 포괄 패턴(1단계)이 한다.
//
// # 판정 순서
//
//  1. 3단계 항목: 방법, 계획, 시점, 준비 행동, 작별 인사
//  2. 2단계 항목: 지금 죽고 싶다는 말, 자해 행동, 살 이유가 없다는 말
//  3. 1단계 항목: 사라지거나 그만두고 싶다는 막연한 말, 깨어나지 않았으면 하는 말, 지난날의 생각, 짐이 된다는 말
//  4. 포괄 패턴(1단계): 죽음과 자해에 관한 낱말. 제외 패턴에 가려지면 걸리지 않은 것으로 본다.
//
// 항목은 unless로 가드와 제외 패턴을 가리킬 수 있다. 그 구간과 겹친 자리는 그 항목에 걸리지 않은 것으로 보고,
// 같은 자리를 아래 순서의 항목과 포괄 패턴이 다시 본다. "죽고 싶진 않아"는 2단계 항목에서는 빠지지만
// 포괄 패턴에는 남아 1단계가 된다. "배고파 죽겠다"는 포괄 패턴에 걸렸다가 관용 표현으로 가려져 걸리지 않은 말이 된다.
//
// # 가려낸 말을 "걸리지 않음"으로 넘기는 이유
//
// 관용 표현을 "걸렸지만 해당 없음"으로 넘기면, AI 판별이 제때 답하지 못한 턴마다 "배고파 죽겠다"에 되묻게 된다.
// 판별의 응답 시간은 가끔 크게 늘어지므로 드문 일이 아니다. 그런 반응을 겪은 사람은 말을 고르기 시작한다.
// 가려낸 말도 AI 판별은 그대로 본다. 무엇이 가렸는지는 Result.ExcludedBy에 남는다.
package rules

import (
	"log/slog"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
)

const (
	// maxEvidenceRunes는 근거로 남기는 글의 최대 길이다. 패턴 하나가 덮는 구간은 짧아서 평소에는 닿지 않는다.
	maxEvidenceRunes = 120
	// 근거를 넓힐 때 한쪽으로 움직이는 최대 글자 수다. 문장 경계는 멀리까지, 어절 경계는 가까이에서만 찾는다.
	// 문장 부호도 공백도 없이 이어 쓴 긴 글에서 근거가 글 전체로 번지지 않게 한다.
	maxSentenceWidenRunes = 40
	maxWordWidenRunes     = 12
	// meTooWindowBytes는 남의 이야기로 본 구간 뒤로 "나도" 같은 말을 찾아보는 거리다. 한글 열두 글자쯤이다.
	meTooWindowBytes = 36
)

// Result는 규칙의 판정이다. Evidence에는 사용자의 말이 들어 있으므로 통째로 로그에 넘기지 않는다.
// 넘기더라도 LogValue가 식별자와 단계만 남긴다.
type Result struct {
	// Stage는 규칙이 본 단계다.
	Stage crisis.Stage
	// Matched는 사전에 걸렸는지다. 포괄 패턴에 걸렸다가 제외 패턴에 가려진 말은 false다.
	Matched bool
	// EntryIDs는 걸린 항목의 식별자다. 단계가 높은 것부터, 같은 단계 안에서는 사전에 적힌 순서다.
	// 가드나 제외 패턴에 가려진 항목은 들어가지 않는다.
	EntryIDs []string
	// ExcludedBy는 포괄 패턴에 걸린 자리를 가려낸 제외 패턴의 식별자다.
	// Matched가 true여도 채워질 수 있다(한 자리는 가려지고 다른 자리는 남은 경우).
	ExcludedBy []string
	// Evidence는 가장 높은 단계의 항목이 걸린 자리를 원문에서 그대로 잘라낸 것이다. 걸리지 않았으면 비어 있다.
	Evidence string
}

// Core는 core/crisis가 받는 꼴로 바꾼다.
func (r Result) Core() crisis.RuleResult {
	return crisis.RuleResult{Stage: r.Stage, Matched: r.Matched}
}

// LogValue는 판정을 통째로 로그에 넘겨도 사용자의 말이 나가지 않게 한다.
func (r Result) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("stage", int(r.Stage)),
		slog.Bool("matched", r.Matched),
		slog.Any("entry_ids", r.EntryIDs),
		slog.Any("excluded_by", r.ExcludedBy),
		slog.Int("evidence_chars", utf8.RuneCountInString(r.Evidence)),
	)
}

type hit struct{ start, end int }

func (h hit) overlaps(o hit) bool {
	return h.start < o.end && o.start < h.end
}

// scan은 발화 하나를 판정하는 동안의 상태다. 같은 가드를 여러 항목이 가리키므로 찾은 구간을 한 번만 구한다.
type scan struct {
	lex   *Lexicon
	text  string
	spans [][]hit
	done  []bool
}

// Scan은 발화 하나를 판정한다. 같은 글에는 언제나 같은 답을 낸다.
//
// 걸리는 시간은 글의 길이에 비례한다. 패턴을 돌리는 Go의 regexp는 되돌아가며 찾지 않으므로
// 어떤 글을 넣어도 시간이 폭발하지 않는다. 글의 길이는 받는 쪽(채널의 메시지 크기 한도)이 묶는다.
func (l *Lexicon) Scan(text string) Result {
	n := normalize(text)
	if n.text == "" {
		return Result{}
	}
	total := len(l.exclusions) + len(l.guards)
	s := &scan{lex: l, text: n.text, spans: make([][]hit, total), done: make([]bool, total)}

	var (
		result Result
		best   hit
		found  bool
	)
	record := func(e entry, hits []hit) {
		result.EntryIDs = append(result.EntryIDs, e.id)
		if e.stage > result.Stage || (e.stage == result.Stage && found && hits[0].start < best.start) {
			result.Stage, best = e.stage, hits[0]
		}
		found = true
	}

	for _, e := range l.entries {
		if hits := s.surviving(e, e.unless); len(hits) > 0 {
			record(e, hits)
		}
	}

	excludedBy := map[string]bool{}
	for _, e := range l.catchAll {
		var kept []hit
		for _, h := range s.find(e.re) {
			if by := s.excludedBy(h); len(by) > 0 {
				for _, id := range by {
					excludedBy[id] = true
				}
				continue
			}
			kept = append(kept, h)
		}
		if len(kept) > 0 {
			record(e, kept)
		}
	}
	for _, ex := range l.exclusions {
		if excludedBy[ex.id] {
			result.ExcludedBy = append(result.ExcludedBy, ex.id)
		}
	}

	if !found {
		return result
	}
	result.Matched = true
	result.Evidence = evidence(text, n, best)
	return result
}

func (s *scan) find(re *regexp.Regexp) []hit {
	locs := re.FindAllStringIndex(s.text, -1)
	if len(locs) == 0 {
		return nil
	}
	hits := make([]hit, 0, len(locs))
	for _, loc := range locs {
		hits = append(hits, hit{loc[0], loc[1]})
	}
	return hits
}

// spansOf는 가드나 제외 패턴이 덮는 구간이다.
//
// 남의 이야기라는 짐작은 앞뒤 낱말만 보고 하는 것이라 틀릴 수 있다. 그래서 구간 안에 말하는 사람 자신을 가리키는 말이 있거나
// 구간 바로 뒤에 자기를 겹쳐 놓는 말이 있으면 그 구간을 쓰지 않는다.
// "드라마 보다가 나도 죽고 싶다는 생각이 들었어"와 "친구가 자살했어. 나도 따라가고 싶어"를 남의 이야기로 넘기지 않으려는 것이다.
// 틀리더라도 단계가 높아지는 쪽으로 틀린다.
func (s *scan) spansOf(ex *exclusion) []hit {
	if s.done[ex.index] {
		return s.spans[ex.index]
	}
	s.done[ex.index] = true

	hits := s.find(ex.re)
	if ex.kind == kindContext {
		kept := hits[:0]
		for _, h := range hits {
			if s.lex.firstPerson.MatchString(s.text[h.start:h.end]) {
				continue
			}
			after := s.text[h.end:min(len(s.text), h.end+meTooWindowBytes)]
			if s.lex.meToo.MatchString(after) {
				continue
			}
			kept = append(kept, h)
		}
		hits = kept
	}
	s.spans[ex.index] = hits
	return hits
}

// surviving은 항목이 걸린 자리 가운데 unless의 어느 구간과도 겹치지 않는 것만 남긴다.
func (s *scan) surviving(e entry, unless []*exclusion) []hit {
	hits := s.find(e.re)
	if len(hits) == 0 || len(unless) == 0 {
		return hits
	}
	kept := hits[:0]
	for _, h := range hits {
		if !s.covered(h, unless) {
			kept = append(kept, h)
		}
	}
	return kept
}

func (s *scan) covered(h hit, by []*exclusion) bool {
	for _, ex := range by {
		for _, span := range s.spansOf(ex) {
			if h.overlaps(span) {
				return true
			}
		}
	}
	return false
}

// excludedBy는 포괄 패턴이 걸린 자리를 가리는 제외 패턴의 식별자를 돌려준다.
func (s *scan) excludedBy(h hit) []string {
	var ids []string
	for _, ex := range s.lex.exclusions {
		for _, span := range s.spansOf(ex) {
			if h.overlaps(span) {
				ids = append(ids, ex.id)
				break
			}
		}
	}
	return ids
}

// evidence는 걸린 자리를 원문에서 잘라낸다. 돌려주는 글은 언제나 원문에 글자 그대로 있는 조각이다.
//
// 패턴이 덮는 구간은 "죽고"처럼 낱말 한 토막일 때가 많아서 그것만 남기면 나중에 읽는 사람이 뜻을 알 수 없다.
// 그래서 가까운 문장 경계까지 넓히고, 문장이 너무 길면 어절 경계까지만 넓힌다.
func evidence(original string, n normalized, h hit) string {
	start, end := n.span(h.start, h.end)
	if start >= end || end > len(original) {
		return ""
	}

	start = widenLeft(original, start)
	end = widenRight(original, end)

	out := strings.TrimSpace(original[start:end])
	if utf8.RuneCountInString(out) > maxEvidenceRunes {
		cut := 0
		for i := range out {
			if cut == maxEvidenceRunes {
				out = out[:i]
				break
			}
			cut++
		}
	}
	return out
}

func sentenceEnd(r rune) bool {
	switch r {
	case '.', '!', '?', '\n', '\r', '…', '。', '！', '？':
		return true
	}
	return false
}

// widenLeft는 문장의 처음까지 물러난다. 한도 안에 문장 경계가 없으면 가까운 공백까지만, 그것도 없으면 물러나지 않는다.
func widenLeft(original string, start int) int {
	pos, word, moved := start, -1, 0
	for {
		if pos == 0 {
			return pos
		}
		r, size := utf8.DecodeLastRuneInString(original[:pos])
		if sentenceEnd(r) {
			return pos
		}
		if unicode.IsSpace(r) && word < 0 && moved <= maxWordWidenRunes {
			word = pos
		}
		if moved == maxSentenceWidenRunes {
			break
		}
		pos -= size
		moved++
	}
	if word >= 0 {
		return word
	}
	return start
}

// widenRight는 문장의 끝까지 나아간다. 끝을 알리는 부호는 근거에 넣는다.
func widenRight(original string, end int) int {
	pos, word, moved := end, -1, 0
	for {
		if pos == len(original) {
			return pos
		}
		r, size := utf8.DecodeRuneInString(original[pos:])
		if sentenceEnd(r) {
			for pos < len(original) {
				next, nextSize := utf8.DecodeRuneInString(original[pos:])
				if !sentenceEnd(next) || next == '\n' || next == '\r' {
					break
				}
				pos += nextSize
			}
			return pos
		}
		if unicode.IsSpace(r) && word < 0 && moved <= maxWordWidenRunes {
			word = pos
		}
		if moved == maxSentenceWidenRunes {
			break
		}
		pos += size
		moved++
	}
	if word >= 0 {
		return word
	}
	return end
}
