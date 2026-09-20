package reply

import (
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Rule은 출력 검사 하나의 이름이다. 로그와 지표에 그대로 쓸 수 있다.
type Rule string

const (
	// RuleEmpty는 말할 글이 없는 경우다.
	RuleEmpty Rule = "empty"
	// RuleTooManySentences는 문장이 한도(두 문장)를 넘은 경우다.
	RuleTooManySentences Rule = "too_many_sentences"
	// RuleTooLong은 문장 수는 맞지만 소리 내어 듣기에 너무 긴 경우다.
	RuleTooLong Rule = "too_long"
	// RuleTooManyQuestions는 한 번에 둘 이상을 물은 경우다.
	RuleTooManyQuestions Rule = "too_many_questions"
	// RuleQuestionNotAllowed는 묻지 않아야 하는 턴에 물은 경우다.
	RuleQuestionNotAllowed Rule = "question_not_allowed"
	// RulePhoneNumber는 전화번호를 말한 경우다. 사용자가 먼저 말한 번호여도 걸린다.
	RulePhoneNumber Rule = "phone_number"
	// RuleReferral은 기관, 전문가, 상담, 병원처럼 안내문에 나오는 말을 먼저 꺼낸 경우다.
	RuleReferral Rule = "referral"
	// RulePermissionAsking은 물어봐도 되는지 허락을 구한 경우다.
	RulePermissionAsking Rule = "permission_asking"
	// RuleSecondPerson은 사용자를 "당신"이라고 부른 경우다.
	RuleSecondPerson Rule = "second_person"
	// RuleDiagnosis는 병 이름이나 진단, 평가의 말을 먼저 꺼냈거나, 맞다 아니다를 판정한 경우다.
	RuleDiagnosis Rule = "diagnosis"
	// RuleSymbol은 이모지, 특수문자, 낱자모, 줄바꿈을 쓴 경우다.
	RuleSymbol Rule = "symbol"
	// RuleForeignScript는 한국어 답에 다른 문자가 섞인 경우다.
	RuleForeignScript Rule = "foreign_script"
	// RuleMissingQuestion은 되물어야 하는 턴(ModeCheck)에 묻지 않은 경우다.
	RuleMissingQuestion Rule = "missing_question"
	// RuleNotMirrored는 되물어야 하는 턴(ModeCheck)에 사용자의 표현을 받지 않은 경우다.
	RuleNotMirrored Rule = "not_mirrored"
	// RuleCounsellingEnding은 지시문이 쓰지 말라고 한 상담 문구의 말끝을 쓴 경우다.
	RuleCounsellingEnding Rule = "counselling_ending"
)

// Violation은 출력 검사에 걸린 곳 하나다.
type Violation struct {
	Rule Rule
	// Detail은 걸린 항목의 고정된 이름이다(사전 항목의 이름, 문자 체계의 이름, 개수).
	// 답의 글자를 옮기지 않는다. 그래서 로그에 그대로 남겨도 된다.
	Detail string
}

// LogValue는 "rule:detail" 꼴의 짧은 이름으로 남긴다.
func (v Violation) LogValue() slog.Value {
	return slog.StringValue(v.String())
}

func (v Violation) String() string {
	if v.Detail == "" {
		return string(v.Rule)
	}
	return string(v.Rule) + ":" + v.Detail
}

// Limits는 출력 검사의 조정 값이다.
type Limits struct {
	// MaxSentences는 한 번의 답에 허용하는 문장 수다.
	MaxSentences int
	// MaxQuestions는 한 번의 답에 허용하는 질문 수다.
	MaxQuestions int
	// MaxRunes는 한 번의 답에 허용하는 글자 수다. 두 문장이어도 이보다 길면 소리로 듣기에 부담이다.
	MaxRunes int
}

// DefaultLimits는 한두 문장, 질문은 하나라는 대화 원칙을 옮긴 값이다.
func DefaultLimits() Limits {
	return Limits{MaxSentences: 2, MaxQuestions: 1, MaxRunes: 120}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxSentences <= 0 {
		l.MaxSentences = d.MaxSentences
	}
	if l.MaxQuestions <= 0 {
		l.MaxQuestions = d.MaxQuestions
	}
	if l.MaxRunes <= 0 {
		l.MaxRunes = d.MaxRunes
	}
	return l
}

// Draft는 검사할 답과, 검사에 필요한 그 턴의 사정이다.
type Draft struct {
	Mode Mode
	Text string
	// NoQuestion이면 이번 답에는 질문이 없어야 한다. ModeCheck에서는 보지 않는다. 그 턴은 물어야 하는 턴이다.
	NoQuestion bool
	// UserTexts는 이 대화에서 사용자가 한 말이다. 사용자가 먼저 꺼낸 낱말을 받아 쓰는 것은 막지 않기 위해 본다.
	UserTexts []string
	// UserWords는 ModeCheck에서 받아 되물어야 하는 사용자의 표현이다. 비어 있으면 UserTexts의 마지막 말로 본다.
	UserWords string
}

// Check는 모델이 만든 답이 나가도 되는지 본다. 걸린 곳이 없으면 nil을 돌려준다.
//
// 순수 함수다. 같은 입력에는 같은 결과가 나온다.
//
// 낱말 사전으로 보는 검사(RuleReferral, RuleDiagnosis의 낱말, RuleForeignScript의 로마자)는 사용자가 이 대화에서
// 먼저 쓴 낱말에는 걸리지 않는다. "오늘 상담 받고 왔어"에 "상담은 어땠어요?"라고 받는 것은 사용자의 말을 따라간 것이지
// 안내문이 아니다. 전화번호와 "맞다, 아니다"의 판정은 사용자가 먼저 말했어도 걸린다.
func Check(d Draft, limits Limits) []Violation {
	limits = limits.withDefaults()
	text := strings.TrimSpace(d.Text)
	if text == "" {
		return []Violation{{Rule: RuleEmpty}}
	}

	var out []Violation
	add := func(rule Rule, detail string) {
		for _, v := range out {
			if v.Rule == rule && v.Detail == detail {
				return
			}
		}
		out = append(out, Violation{Rule: rule, Detail: detail})
	}

	sentences := splitSentences(text)
	counted, questions := 0, 0
	for _, s := range sentences {
		if s.isQuestion() {
			questions++
		}
		if s.counts() {
			counted++
		}
	}
	if counted > limits.MaxSentences {
		add(RuleTooManySentences, strconv.Itoa(counted))
	}
	if n := utf8.RuneCountInString(text); n > limits.MaxRunes {
		add(RuleTooLong, "")
	}
	if questions > limits.MaxQuestions {
		add(RuleTooManyQuestions, strconv.Itoa(questions))
	}

	switch {
	case d.Mode == ModeCheck:
		if questions == 0 {
			add(RuleMissingQuestion, "")
		}
		words := d.UserWords
		if strings.TrimSpace(words) == "" && len(d.UserTexts) > 0 {
			words = d.UserTexts[len(d.UserTexts)-1]
		}
		if !mirrors(text, words) {
			add(RuleNotMirrored, "")
		}
	case d.NoQuestion && questions > 0:
		add(RuleQuestionNotAllowed, "")
	}

	for _, detail := range phoneNumbers(text) {
		add(RulePhoneNumber, detail)
	}
	for _, e := range referralLexicon {
		if e.found(text, d.UserTexts) {
			add(RuleReferral, e.id)
		}
	}
	squeezed := removeSpaces(text)
	for _, p := range permissionPatterns {
		if p.re.MatchString(squeezed) {
			add(RulePermissionAsking, p.id)
		}
	}
	if strings.Contains(text, "당신") {
		add(RuleSecondPerson, "")
	}
	for _, e := range diagnosisLexicon {
		if e.found(text, d.UserTexts) {
			add(RuleDiagnosis, e.id)
		}
	}
	if pastGunyo(text) {
		add(RuleCounsellingEnding, "past_gunyo")
	}
	if verdictPattern.MatchString(squeezed) {
		add(RuleDiagnosis, "verdict")
	}
	for _, detail := range symbols(text) {
		add(RuleSymbol, detail)
	}
	for _, detail := range foreignScripts(text, d.UserTexts) {
		add(RuleForeignScript, detail)
	}
	return out
}

// EndsWithQuestion은 말이 질문으로 끝나는지 본다. 직전의 AI 말이 질문이었는지 가리는 데 쓴다.
func EndsWithQuestion(text string) bool {
	sentences := splitSentences(strings.TrimSpace(text))
	if len(sentences) == 0 {
		return false
	}
	return sentences[len(sentences)-1].isQuestion()
}

// ---- 문장 ---------------------------------------------------------------------

type sentence struct {
	// words는 끝의 문장 부호를 뺀 글이다.
	words string
	// mark는 끝의 문장 부호에 물음표가 있었는지다.
	mark bool
}

// interjectionSyllables 이하의 짧은 토막("와!", "아이고.", "그럼요.")은 문장으로 세지 않는다.
// 감탄사 하나를 끊어 말했다고 답을 버리면, 다시 만드는 몇 초를 사용자가 기다리게 된다.
const interjectionSyllables = 3

func (s sentence) counts() bool {
	return s.isQuestion() || hangulSyllables(s.words) > interjectionSyllables
}

func (s sentence) isQuestion() bool {
	return s.mark || interrogative(s.words)
}

const (
	terminalMarks = ".!?…。！？"
	closingQuotes = `"'”’`
)

// 받침 없이 "요"로 끝나지만 말끝이 아닌 낱말이다. 문장 부호 없이 이어 쓴 글에서 문장을 나눌 때 여기서는 끊지 않는다.
var nounsEndingInYo = map[string]bool{
	"필요": true, "불필요": true, "중요": true, "주요": true, "수요": true, "소요": true,
	"동요": true, "민요": true, "가요": true, "강요": true, "개요": true, "요요": true,
}

// splitSentences는 답을 문장으로 나눈다.
//
// 문장 부호가 기준이다. 다만 문장 부호를 아예 쓰지 않은 글("그랬군요 힘드셨겠어요 오늘은 쉬어요")이
// 한 문장으로 세어져 길이 검사를 빠져나가지 않도록, 해요체의 말끝("~요", "~죠") 뒤의 공백도 문장의 끝으로 본다.
func splitSentences(text string) []sentence {
	runes := []rune(text)
	var out []sentence
	var cur []rune

	flush := func(mark bool) {
		words := strings.TrimSpace(string(cur))
		cur = cur[:0]
		if !strings.ContainsFunc(words, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
			// 문장 부호뿐인 토막이다. 앞 문장에 물음표를 보태는 일만 한다.
			if mark && len(out) > 0 {
				out[len(out)-1].mark = true
			}
			return
		}
		out = append(out, sentence{words: words, mark: mark})
	}

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\n' || r == '\r':
			flush(false)

		case strings.ContainsRune(terminalMarks, r):
			if r == '.' && i > 0 && i+1 < len(runes) && unicode.IsDigit(runes[i-1]) && unicode.IsDigit(runes[i+1]) {
				// "3.5"의 점은 문장의 끝이 아니다.
				cur = append(cur, r)
				continue
			}
			mark := false
			for i < len(runes) && (strings.ContainsRune(terminalMarks, runes[i]) || strings.ContainsRune(closingQuotes, runes[i])) {
				if runes[i] == '?' || runes[i] == '？' {
					mark = true
				}
				i++
			}
			i--
			flush(mark)

		case unicode.IsSpace(r):
			if endsSpokenSentence(cur) {
				flush(false)
				continue
			}
			cur = append(cur, r)

		default:
			cur = append(cur, r)
		}
	}
	flush(false)
	return out
}

// endsSpokenSentence는 지금까지의 글이 해요체의 말끝으로 끝났는지 본다.
// 쉼표로 이은 말("그랬군요, 많이 놀랐겠어요")은 한 문장으로 말한 것이라 끊지 않는다.
func endsSpokenSentence(cur []rune) bool {
	if len(cur) > 0 && cur[len(cur)-1] == ',' {
		return false
	}
	word := lastWord(string(cur))
	runes := []rune(word)
	if len(runes) < 2 || nounsEndingInYo[word] {
		return false
	}
	last := runes[len(runes)-1]
	return last == '요' || last == '죠'
}

func lastWord(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimRight(fields[len(fields)-1], closingQuotes+",")
}

const (
	hangulBase  = 0xAC00
	hangulLast  = 0xD7A3
	finalsCount = 28

	finalNieun  = 4  // ㄴ
	finalRieul  = 8  // ㄹ
	finalBieup  = 17 // ㅂ
	finalSsangS = 20 // ㅆ
)

func isHangulSyllable(r rune) bool { return r >= hangulBase && r <= hangulLast }

func hangulSyllables(s string) int {
	n := 0
	for _, r := range s {
		if isHangulSyllable(r) {
			n++
		}
	}
	return n
}

// final은 글자의 받침 번호다. 한글 음절이 아니면 -1이다.
func final(r rune) int {
	if !isHangulSyllable(r) {
		return -1
	}
	return int(r-hangulBase) % finalsCount
}

// 받침이 ㅆ이지만 지난 일을 가리키지 않는 어간이다. "있군요", "없군요", "재밌군요"는 지금을 말한 것이라 상담 말투가 아니다.
var presentStemsWithSsangS = map[rune]bool{'있': true, '없': true, '밌': true}

// pastGunyo는 지난 일을 가리키는 ㅆ 받침 뒤에 "군요"가 붙은 말끝을 찾는다("다녀왔군요", "계셨군요", "힘들었군요").
// 사용자가 지난 일을 이야기한 첫 턴에서 모델이 미끄러지는 자리라, 지시문으로만 막기에는 자주 나온다.
// 받침이 없거나 다른 받침인 "군요"("그렇군요", "많군요")는 지금을 말한 것이라 잡지 않는다.
func pastGunyo(text string) bool {
	runes := []rune(text)
	for i := 2; i < len(runes); i++ {
		if runes[i-1] != '군' || runes[i] != '요' {
			continue
		}
		if stem := runes[i-2]; final(stem) == finalSsangS && !presentStemsWithSsangS[stem] {
			return true
		}
	}
	return false
}

var indirectQuestion = regexp.MustCompile(`궁금|알고 ?싶`)

// interrogative는 물음표 없이도 묻는 말인지 본다.
//
// 해요체는 글로는 묻는 말과 아닌 말이 같아서("가요"), 꼴만으로 확실한 것만 잡는다.
//   - "~ㄹ까요": 받침 ㄹ 뒤의 "까요". "~니까요"는 까닭을 대는 말이라 묻는 말이 아니다.
//   - "~았/었/겠/셨나요", "~시나요", "~하나요", "~되나요": "생각나요", "화나요"는 묻는 말이 아니다.
//   - "~ㄴ가요": 받침 ㄴ 뒤의 "가요". "학교에 가요"는 묻는 말이 아니다.
//   - "~ㅂ니까"
//   - "궁금해요", "알고 싶어요"로 에둘러 묻는 말.
func interrogative(words string) bool {
	if indirectQuestion.MatchString(words) {
		return true
	}
	runes := []rune(lastWord(words))
	n := len(runes)
	if n < 3 {
		return false
	}
	before, tail := runes[n-3], string(runes[n-2:])
	switch tail {
	case "까요":
		return final(before) == finalRieul
	case "나요":
		return final(before) == finalSsangS || before == '시' || before == '하' || before == '되'
	case "가요":
		return final(before) == finalNieun
	case "니까":
		return final(before) == finalBieup
	}
	return false
}

// ---- 되받기 --------------------------------------------------------------------

// 어느 말에나 있어서 겹쳐도 되받은 것으로 볼 수 없는 토막이다.
// 말끝도 여기에 든다. "좋겠어"와 "힘드셨겠어요"는 "겠어"가 겹치지만 사용자의 표현을 받은 것이 아니다.
var commonBigrams = map[string]bool{
	"오늘": true, "그냥": true, "요즘": true, "진짜": true, "정말": true,
	"너무": true, "이제": true, "지금": true, "무슨": true,
	"겠어": true, "었어": true, "았어": true, "했어": true, "였어": true, "있어": true, "없어": true,
	"어요": true, "아요": true, "에요": true, "예요": true, "네요": true, "으면": true,
	"싶어": true, "싶다": true, "같아": true, "하고": true, "는데": true, "지만": true,
}

// mirrors는 답이 사용자의 표현에서 두 글자 이상을 그대로 받았는지 본다.
// 말끝은 활용하면서 바뀌므로("사라졌으면"을 "사라지고 싶을"로 받는다) 낱말 전체가 아니라 두 글자 토막으로 견준다.
// 견줄 토막이 없는 표현이면 받은 것으로 본다.
func mirrors(text, userWords string) bool {
	grams := bigrams(userWords)
	if len(grams) == 0 {
		return true
	}
	for g := range bigrams(text) {
		if grams[g] {
			return true
		}
	}
	return false
}

func bigrams(s string) map[string]bool {
	out := map[string]bool{}
	var prev rune
	for _, r := range s {
		if isHangulSyllable(r) && isHangulSyllable(prev) {
			if g := string([]rune{prev, r}); !commonBigrams[g] {
				out[g] = true
			}
		}
		prev = r
	}
	return out
}

// ---- 전화번호 ------------------------------------------------------------------

var (
	dashedNumber = regexp.MustCompile(`[0-9]{2,4}[-‐-―][0-9]{3,4}`)
	longNumber   = regexp.MustCompile(`[0-9]{3,}`)
	// 번호를 한 자씩 읽은 꼴이다. "이삼일"이나 "일일이" 같은 흔한 말과 겹치지 않도록, 공(0)이 든 세 글자 이상만 본다.
	spelledNumber = regexp.MustCompile(`[공영일이삼사오육칠팔구]{3,}`)
	// 알려진 번호를 읽은 꼴은 공이 없어도 잡는다.
	knownSpelled = []string{"일일구", "일오칠칠"}
	// 세 자리 이상의 숫자라도 바로 뒤에 이런 말이 오면 번호가 아니라 수량이다. "번"은 넣지 않는다("109번으로").
	quantityUnits = []string{
		"년", "월", "일", "시", "분", "초", "원", "만", "천", "억", "개", "명", "살", "세", "층", "호", "점", "등", "위",
		"쪽", "장", "회", "도", "번째", "킬로", "미터", "그램", "칼로리", "보", "퍼센트", "프로", "배", "권", "편", "통",
		"잔", "마리", "대", "평", "주", "달", "kg", "km", "cm", "g", "m",
	}
)

func phoneNumbers(text string) []string {
	var out []string
	if dashedNumber.MatchString(text) {
		out = append(out, "dashed")
	}
	for _, loc := range longNumber.FindAllStringIndex(text, -1) {
		rest := strings.TrimLeft(text[loc[1]:], " ")
		if !hasAnyPrefix(rest, quantityUnits) {
			out = append(out, "digits")
			break
		}
	}
	for _, m := range spelledNumber.FindAllString(text, -1) {
		if strings.ContainsAny(m, "공영") {
			out = append(out, "spelled")
			break
		}
	}
	for _, k := range knownSpelled {
		if strings.Contains(text, k) {
			out = append(out, "spelled")
			break
		}
	}
	return out
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// ---- 낱말 사전 -----------------------------------------------------------------

type lexEntry struct {
	// id는 로그에 남길 이름이다.
	id string
	re *regexp.Regexp
}

// found는 답에 그 낱말이 있고, 사용자가 먼저 쓴 낱말은 아닌지 본다.
func (e lexEntry) found(text string, userTexts []string) bool {
	if !e.re.MatchString(text) {
		return false
	}
	for _, u := range userTexts {
		if e.re.MatchString(u) {
			return false
		}
	}
	return true
}

func lex(id, pattern string) lexEntry {
	return lexEntry{id: id, re: regexp.MustCompile(pattern)}
}

// referralLexicon은 안내문에 나오는 말이다. 도움받을 곳을 알리는 일은 미리 써 둔 문구가 맡는다.
// 낱말마다 항목을 따로 둔다. 사용자가 "병원"을 말했다고 해서 모델이 "정신과"를 꺼내도 되는 것은 아니다.
var referralLexicon = []lexEntry{
	lex("counsel", `상담`),
	lex("expert", `전문가`),
	lex("specialist", `전문의`),
	lex("doctor", `의사 ?선생|주치의`),
	lex("center", `센터`),
	lex("institution", `기관`),
	lex("hospital", `병원`),
	lex("psychiatry", `정신과|정신건강의학과`),
	lex("clinic", `클리닉`),
	lex("health_office", `보건소`),
	lex("hotline", `핫라인|헬프라인`),
	lex("treatment", `치료`),
	lex("medical_care", `진료`),
	lex("prescription", `처방`),
	lex("seek_help", `도움을? ?받`),
}

// diagnosisLexicon은 병 이름과 진단, 평가의 말이다.
var diagnosisLexicon = []lexEntry{
	lex("depression", `우울증`),
	lex("bipolar", `조울증`),
	lex("insomnia", `불면증`),
	lex("panic_disorder", `공황장애`),
	lex("anxiety_disorder", `불안장애`),
	lex("sleep_disorder", `수면장애`),
	lex("syndrome", `증후군`),
	lex("acronym", `PTSD|ADHD`),
	lex("symptom", `증상`),
	lex("diagnosis", `진단`),
	lex("illness", `질환|정신병`),
	lex("patient", `환자`),
	lex("risk_level", `위험도|우울도`),
	lex("severity", `중증|경증`),
	lex("verdict_word", `판정`),
	lex("abnormal", `비정상`),
}

// verdictPattern은 병 이름을 두고 맞다, 아니다, 그런 것 같다고 판정하는 말이다. 공백을 뺀 글에서 찾는다.
// 사용자가 먼저 병 이름을 꺼냈어도 걸린다. "우울증이 맞는지 걱정되는군요"처럼 판정하지 않고 받는 말은 걸리지 않는다.
var verdictPattern = regexp.MustCompile(
	`(우울증|조울증|불면증|공황장애|불안장애|수면장애|번아웃|질환)` +
		`((이|가|은|는|도)?(맞아요|맞습니다|맞네요|맞죠|맞는것|맞는거|맞을|아니에요|아닙니다|아니네요|아니죠|아닌것|아닌거|아닐거|아닐수|의심)` +
		`|(인|일)(것같|거같|수있|수도|가능성|듯해|듯하)` +
		`|(으로|로|처럼)보(여|입|이네))`,
)

type idPattern struct {
	id string
	re *regexp.Regexp
}

// permissionPatterns는 공백을 뺀 글에서 찾는다.
var permissionPatterns = []idPattern{
	{"may_i_ask", regexp.MustCompile(`(여쭤|여쭈어|물어|질문|말씀)[가-힣]{0,5}도(될까요|될지|되나요|돼요|괜찮을까요|괜찮을지|괜찮나요|괜찮아요)`)},
	{"if_you_dont_mind", regexp.MustCompile(`괜찮으시다면|괜찮다면|실례가안된다면|실례지만|불편하지않으시다면|불편하지않다면`)},
}

func removeSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// ---- 글자 ---------------------------------------------------------------------

const allowedPunctuation = `.,!?…'"‘’“”`

// symbols는 말로 읽을 수 없는 글자를 찾는다. 허용하는 것을 정해 두고 나머지를 모두 잡는다.
func symbols(text string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(detail string) {
		if !seen[detail] {
			seen[detail] = true
			out = append(out, detail)
		}
	}
	for _, r := range text {
		switch {
		case r == ' ', isHangulSyllable(r), r >= '0' && r <= '9', strings.ContainsRune(allowedPunctuation, r):
		case r == '\n' || r == '\r' || r == '\t':
			add("line_break")
		case isJamo(r):
			add("jamo")
		case unicode.IsLetter(r), unicode.IsMark(r) && !isEmojiJoiner(r):
			// 글자는 문자 체계 검사가 본다.
		case isEmoji(r):
			add("emoji")
		default:
			add("punctuation")
		}
	}
	return out
}

// isJamo는 음절을 이루지 못한 낱자모다("ㅋㅋ", "ㅠㅠ"). 음성으로 읽을 수 없다.
func isJamo(r rune) bool {
	return (r >= 0x3131 && r <= 0x318E) || (r >= 0x1100 && r <= 0x11FF) || (r >= 0xA960 && r <= 0xA97F) || (r >= 0xD7B0 && r <= 0xD7FF)
}

func isEmojiJoiner(r rune) bool {
	return r == 0x200D || (r >= 0xFE00 && r <= 0xFE0F)
}

func isEmoji(r rune) bool {
	switch {
	case isEmojiJoiner(r):
		return true
	case r >= 0x1F000 && r <= 0x1FAFF, r >= 0x2600 && r <= 0x27BF, r >= 0x2B00 && r <= 0x2BFF, r >= 0x2190 && r <= 0x21FF:
		return true
	case r >= 0x1F1E6 && r <= 0x1F1FF, r >= 0xE0020 && r <= 0xE007F:
		return true
	}
	return unicode.Is(unicode.So, r)
}

var namedScripts = []struct {
	name  string
	table *unicode.RangeTable
}{
	{"han", unicode.Han},
	{"hiragana", unicode.Hiragana},
	{"katakana", unicode.Katakana},
	{"cyrillic", unicode.Cyrillic},
	{"greek", unicode.Greek},
	{"arabic", unicode.Arabic},
	{"hebrew", unicode.Hebrew},
	{"thai", unicode.Thai},
	{"devanagari", unicode.Devanagari},
}

// maxAbbreviationLetters는 사용자가 쓰지 않았어도 받아들이는 로마자 약어의 최대 길이다("PPT", "MBTI").
const maxAbbreviationLetters = 5

var latinWord = regexp.MustCompile(`\p{Latin}+`)

// foreignScripts는 한국어 답에 섞인 다른 문자를 찾는다.
//
// 한자, 가나, 키릴 문자처럼 한글도 로마자도 아닌 글자는 하나만 있어도 걸린다. 모델이 다른 언어로 미끄러진 것이다.
// 로마자는 한국어 말에서 흔히 쓰이므로 낱말 단위로 본다. 다음 둘은 받아들인다.
//   - 사용자가 이 대화에서 쓴 낱말(대소문자는 가리지 않는다). 사용자의 낱말을 그대로 받는 것이 원칙이다.
//   - 대문자로만 된 다섯 글자 이하의 약어("PPT", "TV", "MBTI").
//
// 그 밖의 로마자 낱말은 영어로 미끄러진 것으로 본다("so so", "That sounds tough").
func foreignScripts(text string, userTexts []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(detail string) {
		if !seen[detail] {
			seen[detail] = true
			out = append(out, detail)
		}
	}

	for _, r := range text {
		if !unicode.IsLetter(r) || isHangulSyllable(r) || isJamo(r) || unicode.Is(unicode.Latin, r) {
			continue
		}
		add(scriptName(r))
	}

	var userLatin map[string]bool
	for _, word := range latinWord.FindAllString(text, -1) {
		if isAbbreviation(word) {
			continue
		}
		if userLatin == nil {
			userLatin = map[string]bool{}
			for _, u := range userTexts {
				for _, w := range latinWord.FindAllString(u, -1) {
					userLatin[strings.ToLower(w)] = true
				}
			}
		}
		if !userLatin[strings.ToLower(word)] {
			add("latin")
		}
	}
	return out
}

// scriptName은 글자가 속한 문자 체계의 이름이다.
// 가나의 긴소리표("ー")처럼 여러 문자가 함께 쓰는 글자는 유니코드에서 어느 체계에도 속하지 않으므로, 가나 영역은 자리로 가린다.
func scriptName(r rune) string {
	switch {
	case r >= 0x3040 && r <= 0x309F:
		return "hiragana"
	case r >= 0x30A0 && r <= 0x30FF, r >= 0xFF66 && r <= 0xFF9F:
		return "katakana"
	}
	for _, s := range namedScripts {
		if unicode.Is(s.table, r) {
			return s.name
		}
	}
	return "other"
}

func isAbbreviation(word string) bool {
	if utf8.RuneCountInString(word) > maxAbbreviationLetters {
		return false
	}
	for _, r := range word {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
