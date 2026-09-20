package crypto

import "errors"

var (
	// ErrDecrypt는 인증에 실패했다는 뜻이다. 암호문이 바뀌었거나, 다른 사용자의 키이거나,
	// 다른 행이나 컬럼의 암호문을 가져다 놓은 경우다.
	// 어느 쪽인지는 구분하지 않는다. GCM이 구분해 주지 않고, 구분하면 공격하는 쪽에 단서가 된다.
	ErrDecrypt = errors.New("crypto: decryption failed")

	// ErrFormat은 열어 보기도 전에 꼴이 틀렸다는 뜻이다. 잘렸거나 모르는 판 번호다.
	ErrFormat = errors.New("crypto: malformed ciphertext")

	// ErrKeyVersion은 그 버전의 마스터 키를 들고 있지 않다는 뜻이다.
	// 옛 마스터 키를 설정에서 너무 일찍 뺀 경우에 나온다.
	ErrKeyVersion = errors.New("crypto: unknown key version")

	// ErrInvalidKey는 키 자체가 쓸 수 없는 값이라는 뜻이다. 길이가 틀렸거나 base64가 아니다.
	ErrInvalidKey = errors.New("crypto: invalid key")

	// ErrInvalidAAD는 암호문을 묶을 자리가 제대로 채워지지 않았다는 뜻이다.
	// 잠그기 전에 행 ID를 만들지 않은 실수를 여기서 잡는다.
	ErrInvalidAAD = errors.New("crypto: invalid associated data")
)
