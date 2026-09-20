package auth

import (
	"net/mail"
	"strings"
)

const (
	// 메일을 주고받는 규약이 정한 주소 전체의 최대 길이다.
	maxEmailLength = 254
	// '@' 앞부분의 최대 길이다.
	maxEmailLocalLength = 64
	// 도메인의 점 사이 한 마디의 최대 길이다.
	maxDomainLabelLength = 63
)

// NormalizeEmail은 이메일 주소를 다듬어 저장하고 찾을 때 쓰는 꼴로 돌려준다.
// 앞뒤 공백을 떼고 소문자로 바꾼다. 꼴이 틀리면 ErrInvalidEmail이다. 오류에는 주소를 담지 않는다.
//
// 받는 꼴은 일부러 좁다.
//   - "이름 <주소>"나 "<주소>"처럼 주소 말고 다른 것이 붙은 꼴은 받지 않는다. 가입 칸에 들어올 이유가 없다.
//   - 따옴표로 묶은 앞부분, 대괄호로 적은 IP 주소, 점이 없는 도메인은 받지 않는다.
//   - ASCII가 아닌 글자는 받지 않는다. 눈으로는 같아 보이는 다른 글자로 남의 주소와 닮은 계정을 만들 수 있다.
func NormalizeEmail(raw string) (string, error) {
	address := strings.TrimSpace(raw)
	if address == "" || len(address) > maxEmailLength {
		return "", ErrInvalidEmail
	}
	for i := range len(address) {
		// 공백, 제어 문자, ASCII 밖의 바이트다.
		if c := address[i]; c <= ' ' || c >= 0x7f {
			return "", ErrInvalidEmail
		}
	}

	parsed, err := mail.ParseAddress(address)
	if err != nil {
		// 파서의 오류에는 주소의 일부가 들어 있다. 붙이지 않는다.
		return "", ErrInvalidEmail
	}
	// 파서가 읽어 낸 주소가 받은 글자와 똑같을 때만 주소 하나만 온 것이다.
	if parsed.Name != "" || parsed.Address != address {
		return "", ErrInvalidEmail
	}

	at := strings.LastIndexByte(address, '@')
	if at < 1 || at > maxEmailLocalLength {
		return "", ErrInvalidEmail
	}
	if !validDomain(address[at+1:]) {
		return "", ErrInvalidEmail
	}
	return strings.ToLower(address), nil
}

// validDomain은 점으로 나뉜 마디가 둘 이상이고, 마디마다 글자, 숫자, '-'만 쓰는지 본다.
func validDomain(domain string) bool {
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > maxDomainLabelLength {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := range len(label) {
			c := label[i]
			letter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
			digit := c >= '0' && c <= '9'
			if !letter && !digit && c != '-' {
				return false
			}
		}
	}
	// 맨 끝 마디가 숫자뿐이면 도메인이 아니라 IP 주소를 적은 것이다.
	last := labels[len(labels)-1]
	for i := range len(last) {
		if last[i] < '0' || last[i] > '9' {
			return true
		}
	}
	return false
}

// emailLocalPart는 다듬은 주소의 '@' 앞부분이다.
func emailLocalPart(email string) string {
	if at := strings.LastIndexByte(email, '@'); at > 0 {
		return email[:at]
	}
	return ""
}
