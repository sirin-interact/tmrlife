// Package sealing은 사용자의 글을 잠그고 여는 쪽이 필요로 하는 두 가지를 내준다.
// 그 사용자의 Sealer와, 글이 놓이는 자리(crypto.AAD)다.
//
// # 왜 따로 있는가
//
// Sealer 하나를 얻으려면 세 가지가 맞물려야 한다. 키 행을 읽는 쿼리(store), 그것을 푸는 마스터 키 묶음과
// 풀어 둔 키를 들고 있는 캐시(crypto)다. 대화 엔진, 일기 초안 작업, 일기 API가 모두 같은 일을 해야 하는데,
// 저마다 맞물리게 두면 캐시를 거치지 않거나 계정을 지운 뒤에 키를 잊지 않는 곳이 생긴다.
//
//   - crypto에 두지 않는다. crypto는 DB를 모르는 순수한 암호화 계층이고, 키 행을 어디서 읽는지 알아서는 안 된다.
//   - store에 두지 않는다. store는 평문도 키도 모른다. 암호문만 주고받는다.
//   - auth에 두지 않는다. auth는 가입할 때 키를 만들 뿐이고, 기록을 읽고 쓰는 쪽은 로그인 절차에 기댈 이유가 없다.
//
// 그래서 crypto와 store를 함께 아는 가장 얇은 자리를 하나 둔다. 프로세스에 하나만 만들어(app.Deps) 모두가 같은 캐시를 본다.
//
// # 자리
//
// 암호문은 테이블 이름, 컬럼 이름, 행 ID에 묶인다. 잠글 때와 열 때 이 셋이 글자 그대로 같아야 열린다.
// 쓰는 쪽마다 문자열을 직접 적으면 오타 하나로 열리지 않는 글이 저장되고, 그 사실은 나중에 읽을 때에야 드러난다.
// 자리는 이 패키지의 함수로만 만든다. 스키마의 _enc 컬럼마다 함수가 하나씩 있고, 시험이 둘을 견준다.
package sealing

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// ErrNoKey는 그 사용자의 데이터 키가 없다는 뜻이다. 계정이 지워진 경우다.
// 키가 없으면 그 사용자의 글은 더는 읽을 수도, 새로 남길 수도 없다. 받은 쪽은 하던 일을 그만두고 다시 시도하지 않는다.
var ErrNoKey = errors.New("sealing: user has no data key")

// KeyReader는 저장된 키 행을 읽는다. *db.Queries가 이 모양을 만족한다.
type KeyReader interface {
	GetUserKey(ctx context.Context, userID uuid.UUID) (db.UserKey, error)
}

// Sealers는 사용자별 Sealer를 내준다. 여러 고루틴이 함께 써도 된다.
type Sealers struct {
	keys  KeyReader
	ring  *crypto.KeyRing
	cache *crypto.KeyCache
}

// New는 Sealers를 만든다. cache는 프로세스에 하나뿐인 것을 넘긴다.
// 캐시가 둘이면 계정을 지울 때 한쪽에서만 키를 잊는다.
func New(keys KeyReader, ring *crypto.KeyRing, cache *crypto.KeyCache) (*Sealers, error) {
	switch {
	case keys == nil:
		return nil, errors.New("sealing: key reader is required")
	case ring == nil:
		return nil, errors.New("sealing: key ring is required")
	case cache == nil:
		return nil, errors.New("sealing: key cache is required")
	}
	return &Sealers{keys: keys, ring: ring, cache: cache}, nil
}

// For는 그 사용자의 Sealer를 돌려준다. 캐시에 없으면 키 행을 읽어 푼다.
//
// 돌려받은 Sealer는 한 요청, 한 턴, 한 작업 동안만 들고 있고 다음에는 다시 받는다.
// 오래 들고 있으면 계정을 지운 뒤에도 그 키로 글을 남기게 된다.
//
//   - 키 행이 없으면 ErrNoKey
//   - 키 행을 풀 수 없으면 crypto의 오류(ErrKeyVersion, ErrDecrypt, ErrFormat)가 그대로 나온다.
//     마스터 키 설정이 틀렸다는 뜻이므로 다시 시도해도 풀리지 않는다.
func (s *Sealers) For(ctx context.Context, userID uuid.UUID) (*crypto.Sealer, error) {
	if userID == uuid.Nil {
		return nil, errors.New("sealing: user id is empty")
	}
	sealer, err := s.cache.Get(ctx, userID, func(ctx context.Context) (*crypto.Sealer, error) {
		row, err := s.keys.GetUserKey(ctx, userID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("%w: %w", ErrNoKey, err)
			}
			return nil, fmt.Errorf("read user key: %w", err)
		}
		return s.ring.Unwrap(userID, row.WrappedDEK, int(row.KEKVersion))
	})
	if err != nil {
		return nil, fmt.Errorf("sealing: %w", err)
	}
	return sealer, nil
}

// Forget은 풀어 둔 키를 이 프로세스에서 잊는다. 계정을 지울 때, 키 행을 지운 다음에 부른다.
// 순서가 바뀌면 그 사이에 들어온 요청이 아직 남아 있는 키 행을 읽어 캐시에 다시 넣는다.
//
// 다른 프로세스에는 닿지 않는다. 다른 프로세스는 캐시의 최대 보관 시간이 지나 키 행을 다시 읽을 때 계정이 지워진 것을 안다.
func (s *Sealers) Forget(userID uuid.UUID) {
	s.cache.Forget(userID)
}
