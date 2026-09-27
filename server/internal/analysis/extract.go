package analysis

import (
	"context"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/logging"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
)

// 모델에 보내는 글의 뼈대다. 지시문(prompts/signal_extract)이 이 낱말들을 그대로 가리키므로 함께 고친다.
const (
	labelTranscript = "대화 기록:"
	labelAssistant  = "[상대] "
)

// 근거를 받아들이지 못한 까닭이다. 로그와 결과에 그대로 쓰는 고정된 이름이고, 근거의 내용은 담지 않는다.
const (
	// dropNoEvidence는 언급된 항목인데 근거가 비어 있는 경우다.
	dropNoEvidence = "no_evidence"
	// dropNotVerbatim은 근거가 사용자의 어느 발화에도 글자 그대로 없는 경우다. 지어낸 근거와 상대의 말이 여기로 온다.
	dropNotVerbatim = "not_verbatim"
	// dropNoLetters는 근거에 글자와 숫자가 하나도 없는 경우다. 어떤 발화에나 들어 있어서 대조가 되지 않는다.
	dropNoLetters = "no_letters"
	// dropGateFlagged는 위기 관문에 걸린 발화에서만 찾히는 근거다. 자해와 죽음에 관한 표현은 점수로 쌓지 않는다.
	dropGateFlagged = "gate_flagged"
	// dropBadStatus와 dropBadExplicitness는 정해진 값이 아닌 판단이 온 경우다.
	dropBadStatus       = "bad_status"
	dropBadExplicitness = "bad_explicitness"
)

// allDropReasons는 로그에 적는 순서다.
var allDropReasons = []string{
	dropNoEvidence, dropNotVerbatim, dropNoLetters, dropGateFlagged, dropBadStatus, dropBadExplicitness,
}

// itemReply는 항목 하나에 대한 모델의 답이다.
type itemReply struct {
	Status       string `json:"status"`
	Explicitness string `json:"explicitness"`
	// Line은 근거를 옮긴 사용자 줄의 번호다. 맞지 않아도 글자가 맞으면 받아 준다.
	Line     int    `json:"line"`
	Evidence string `json:"evidence"`
}

// extracted는 저장할 판단과, 그 판단이 어떻게 나왔는지의 셈이다.
type extracted struct {
	judgements   [signal.ItemCount]store.SignalJudgement
	counts       Counts
	dropped      int
	dropReasons  map[string]int
	repaired     int
	lineMismatch int
	unanchored   int
	model        string
}

func (e *extracted) drop(reason string) {
	e.dropped++
	if e.dropReasons == nil {
		e.dropReasons = map[string]int{}
	}
	e.dropReasons[reason]++
}

// extract는 대화 기록을 모델에 보내 여덟 항목의 판단을 받고, 근거를 대조해 저장할 꼴로 만든다.
func (s *Service) extract(ctx context.Context, m *material) (extracted, error) {
	req := s.prompt.Request([]ai.Message{{Role: ai.RoleUser, Text: buildMessage(m.transcript)}})
	req.MaxOutputTokens = s.maxOutputTokens
	req.Thinking = s.thinking

	callCtx, cancel := context.WithTimeout(ctx, s.callTimeout)
	defer cancel()
	resp, err := s.llm.Generate(callCtx, req)
	if err != nil {
		return extracted{}, err
	}

	var reply map[string]itemReply
	if err := ai.DecodeJSON(req, resp, &reply); err != nil {
		return extracted{}, err
	}
	out, err := m.judge(reply)
	if err != nil {
		return extracted{}, err
	}
	out.model = resp.Model
	return out, nil
}

// buildMessage는 대화를 줄마다 늘어놓는다. 사용자의 말에만 번호를 붙여, 근거를 옮길 곳이 어디인지 번호로 가릴 수 있게 한다.
func buildMessage(transcript []turn) string {
	var b strings.Builder
	b.WriteString(labelTranscript)
	for _, t := range transcript {
		b.WriteString("\n")
		if t.number == 0 {
			b.WriteString(labelAssistant)
			b.WriteString(string(t.text))
			continue
		}
		b.WriteString(strconv.Itoa(t.number))
		b.WriteString(". ")
		b.WriteString(string(t.text))
	}
	return b.String()
}

// judge는 모델의 답을 저장할 판단으로 바꾼다.
//
// 항목 하나가 이상해도 나머지 일곱은 살린다. 근거가 대조를 통과하지 못한 항목은 언급 없음으로 되돌린다.
// 다만 여덟 키 가운데 하나라도 아예 오지 않았으면 답 전체를 버린다.
// 키가 없는 것과 "언급 없음"은 다르다. 앞은 모델이 답하지 않은 것이고 뒤는 모델의 판단이다.
func (m *material) judge(reply map[string]itemReply) (extracted, error) {
	var out extracted
	for _, item := range signal.AllItems() {
		raw, ok := reply[item.String()]
		if !ok {
			return extracted{}, &RejectError{Reason: "missing_item"}
		}
		out.judgements[item.Index()] = m.judgeItem(raw, &out)
	}
	out.counts = judgementCounts(out.judgements)
	return out, nil
}

// judgeItem은 항목 하나의 답을 본다. 받아들일 수 없는 것은 언급 없음으로 되돌린다.
func (m *material) judgeItem(raw itemReply, out *extracted) store.SignalJudgement {
	status, err := signal.ParseStatus(strings.TrimSpace(raw.Status))
	if err != nil {
		out.drop(dropBadStatus)
		return store.SignalJudgement{}
	}
	if !status.Mentioned() {
		// 언급 없음에는 근거가 붙을 수 없다. 근거를 함께 보내왔으면 그 근거만 버리고 판단은 그대로 둔다.
		// 근거가 있다고 관찰됨으로 올리지 않는다. 판단을 올리는 일은 모델만 한다.
		if raw.Evidence != "" || raw.Explicitness != signal.None.String() {
			out.repaired++
		}
		return store.SignalJudgement{}
	}

	explicitness, err := signal.ParseExplicitness(strings.TrimSpace(raw.Explicitness))
	if err != nil {
		out.drop(dropBadExplicitness)
		return store.SignalJudgement{}
	}

	quote := oneLine(raw.Evidence)
	switch {
	case quote == "":
		out.drop(dropNoEvidence)
		return store.SignalJudgement{}
	case !hasLetterOrDigit(quote):
		out.drop(dropNoLetters)
		return store.SignalJudgement{}
	}
	match := m.findQuote(quote, raw.Line)
	if !match.found {
		if match.flaggedOnly {
			out.drop(dropGateFlagged)
			return store.SignalJudgement{}
		}
		out.drop(dropNotVerbatim)
		return store.SignalJudgement{}
	}
	if !match.hinted {
		out.lineMismatch++
	}

	if explicitness == signal.None {
		// 근거는 맞았는데 명시성이 앞뒤가 맞지 않는다. 근거가 있는 판단이므로 버리지 않고 낮은 쪽으로 고친다.
		explicitness = signal.Indirect
		out.repaired++
	}

	judgement := store.SignalJudgement{
		Judgement:    signal.Judgement{Status: status, Explicitness: explicitness},
		SealEvidence: sealEvidence(m.sealer, quote),
	}
	if match.ambiguous {
		// 같은 글자가 사용자의 여러 발화에 있고 모델이 가리킨 줄은 빗나갔다. 어느 말에서 온 토막인지 가릴 수 없다.
		// 판단과 근거는 살리고 발화만 가리키지 않는다. 엉뚱한 발화를 가리키면 근거 화면이
		// 그 말을 한 적 없는 자리에 인용을 붙이고, 발화의 시각이나 순서를 함께 보여주는 화면에서 그 어긋남이 그대로 드러난다.
		out.unanchored++
		return judgement
	}
	utteranceID := match.line.utteranceID
	judgement.EvidenceUtteranceID = &utteranceID
	return judgement
}

// quoteMatch는 근거 토막을 어디에서 찾았는지다.
type quoteMatch struct {
	line userLine
	// hinted는 모델이 가리킨 줄에서 찾았는지다.
	hinted bool
	// found는 근거로 쓸 수 있는 줄에서 찾았는지다.
	found bool
	// ambiguous는 가리킨 줄이 빗나갔고 그 토막이 여러 줄에 있다는 뜻이다. line은 그 가운데 첫 줄이지만 믿을 수 없다.
	ambiguous bool
	// flaggedOnly는 위기 관문에 걸린 줄에서만 찾았다는 뜻이다. 버린 까닭을 가려내는 데 쓴다.
	flaggedOnly bool
}

// findQuote는 근거 토막이 사용자가 한 어느 한 발화에 글자 그대로 있는지 본다.
//
// 모델이 가리킨 줄을 먼저 보고, 그 줄에 없으면 나머지 줄을 번호순으로 본다.
// 두 발화에 걸친 토막은 어느 줄에서도 찾히지 않으므로 받아들여지지 않는다.
// 위기 관문에 걸린 줄은 근거가 될 수 없다. 그 줄에서만 찾힌 토막은 없는 것과 같이 다룬다.
//
// 가리킨 줄이 빗나갔을 때는 남은 줄을 끝까지 본다. 같은 글자가 두 줄 이상에 있으면 어느 말에서 온 토막인지
// 가릴 수 없으므로 ambiguous로 표시한다. "응", "어", "아니" 같은 짧은 답은 한 대화에 여러 번 나오고
// 더 긴 낱말의 일부로도 걸려서("어"는 "없었어" 안에도 있다), 첫 줄을 골라 두면 그 말을 하지 않은 발화에 근거가 매달린다.
func (m *material) findQuote(quote string, hint int) quoteMatch {
	if hint >= 1 && hint <= len(m.lines) {
		if candidate := m.lines[hint-1]; !candidate.gateFlagged && strings.Contains(string(candidate.text), quote) {
			return quoteMatch{line: candidate, hinted: true, found: true}
		}
	}
	var match quoteMatch
	for _, candidate := range m.lines {
		if !strings.Contains(string(candidate.text), quote) {
			continue
		}
		if candidate.gateFlagged {
			match.flaggedOnly = true
			continue
		}
		if match.found {
			match.ambiguous = true
			break
		}
		match.line = candidate
		match.found = true
	}
	if !match.found {
		return quoteMatch{flaggedOnly: match.flaggedOnly}
	}
	match.flaggedOnly = false
	return match
}

// hasLetterOrDigit은 대조할 만한 글자가 있는지 본다.
func hasLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// sealEvidence는 근거 토막을 신호 행의 자리에 묶어 잠그는 함수를 만든다. 행 ID는 저장할 때 정해진다.
func sealEvidence(sealer *crypto.Sealer, quote string) store.SealFunc {
	text := logging.Redacted(quote)
	return func(rowID uuid.UUID) ([]byte, error) {
		return sealer.SealString(string(text), sealing.SignalEvidence(rowID))
	}
}
