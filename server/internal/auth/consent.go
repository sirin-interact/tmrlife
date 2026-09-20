package auth

import (
	"slices"
	"strings"

	"github.com/sirin-interact/tmrlife/server/internal/store"
)

// 동의 문서의 지금 판이다. 문서의 내용이 바뀌면 그날의 날짜로 올린다.
// 판을 올리면 새로 가입하는 사람은 새 판에 동의해야 하고, 이미 가입한 사람은 옛 판에 동의한 기록이 그대로 남는다.
//
// 판을 적는 곳은 여기 하나다. 화면은 CurrentConsents로 받아서 보여주고 그 값을 그대로 돌려보낸다.
const (
	TermsVersion            = "2026-09-20"
	PrivacyVersion          = "2026-09-20"
	SensitiveDataVersion    = "2026-09-20"
	OverseasTransferVersion = "2026-09-20"
)

// ConsentGrant는 사용자가 동의한 문서 하나다. 어느 판을 보고 동의했는지를 함께 받는다.
type ConsentGrant struct {
	Kind    string
	Version string
}

// CurrentConsents는 가입에 필요한 동의와 그 지금 판이다. 넷 모두 필수다.
// 마음에 관한 기록을 다루고, 그 내용이 해외의 AI 서비스로 전송되므로 어느 하나 없이도 서비스를 쓸 수 없다.
func CurrentConsents() []ConsentGrant {
	return []ConsentGrant{
		{Kind: store.ConsentTerms, Version: TermsVersion},
		{Kind: store.ConsentPrivacy, Version: PrivacyVersion},
		{Kind: store.ConsentSensitiveData, Version: SensitiveDataVersion},
		{Kind: store.ConsentOverseasTransfer, Version: OverseasTransferVersion},
	}
}

// ConsentError는 가입 요청의 동의에서 무엇이 틀렸는지다. 값은 동의의 종류다.
// errors.Is(err, ErrConsentRequired)가 참이다.
type ConsentError struct {
	// Missing은 필요한데 오지 않은 동의다.
	Missing []string
	// Outdated는 왔지만 지금 판이 아닌 동의다. 화면이 옛 문서를 보여주고 있었다는 뜻이다.
	Outdated []string
	// Unknown은 모르는 종류로 온 동의의 수다. 그 이름은 요청에서 온 글자라서 옮겨 담지 않는다.
	Unknown int
}

func (e *ConsentError) Error() string {
	var parts []string
	if len(e.Missing) > 0 {
		parts = append(parts, "missing "+strings.Join(e.Missing, ", "))
	}
	if len(e.Outdated) > 0 {
		parts = append(parts, "outdated "+strings.Join(e.Outdated, ", "))
	}
	if e.Unknown > 0 {
		parts = append(parts, "unknown kind")
	}
	return ErrConsentRequired.Error() + ": " + strings.Join(parts, "; ")
}

func (e *ConsentError) Is(target error) bool {
	return target == ErrConsentRequired
}

// checkConsents는 필요한 동의가 모두 지금 판으로 왔는지 본다. 맞으면 저장할 동의를 돌려준다.
// 같은 종류가 여러 번 오면 하나라도 지금 판이면 된 것으로 친다.
func checkConsents(given []ConsentGrant) ([]ConsentGrant, error) {
	required := CurrentConsents()

	known := make(map[string]string, len(required))
	for _, r := range required {
		known[r.Kind] = r.Version
	}

	var problem ConsentError
	current := make(map[string]bool, len(required))
	seen := make(map[string]bool, len(required))
	for _, g := range given {
		version, ok := known[g.Kind]
		if !ok {
			problem.Unknown++
			continue
		}
		seen[g.Kind] = true
		if g.Version == version {
			current[g.Kind] = true
		}
	}
	for _, r := range required {
		switch {
		case current[r.Kind]:
		case seen[r.Kind]:
			problem.Outdated = append(problem.Outdated, r.Kind)
		default:
			problem.Missing = append(problem.Missing, r.Kind)
		}
	}

	if len(problem.Missing)+len(problem.Outdated)+problem.Unknown > 0 {
		slices.Sort(problem.Missing)
		slices.Sort(problem.Outdated)
		return nil, &problem
	}
	return required, nil
}
