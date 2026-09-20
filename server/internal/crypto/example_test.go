package crypto_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/crypto"
)

// keyRow는 DB의 키 테이블 한 행을 흉내 낸다.
type keyRow struct {
	wrapped    []byte
	kekVersion int
}

// 가입할 때 키를 만들어 저장하고, 요청마다 캐시를 거쳐 Sealer를 받아 쓰고,
// 계정을 지울 때 키 행을 지우는 흐름이다.
func Example() {
	ctx := context.Background()
	ring, err := crypto.NewKeyRing(1, map[int][]byte{1: bytes.Repeat([]byte{0x07}, 32)})
	if err != nil {
		fmt.Println(err)
		return
	}
	cache, err := crypto.NewKeyCache(crypto.KeyCacheOptions{MaxEntries: 128})
	if err != nil {
		fmt.Println(err)
		return
	}
	keyTable := map[uuid.UUID]keyRow{}
	errNoKeyRow := errors.New("no key row")

	sealerFor := func(userID uuid.UUID) (*crypto.Sealer, error) {
		return cache.Get(ctx, userID, func(context.Context) (*crypto.Sealer, error) {
			row, ok := keyTable[userID]
			if !ok {
				return nil, errNoKeyRow
			}
			return ring.Unwrap(userID, row.wrapped, row.kekVersion)
		})
	}

	// 가입
	userID := uuid.Must(uuid.NewV7())
	userKey, err := ring.NewUserKey(userID)
	if err != nil {
		fmt.Println(err)
		return
	}
	keyTable[userID] = keyRow{wrapped: userKey.Wrapped, kekVersion: userKey.KEKVersion}

	// 쓰기: 행 ID를 먼저 만들고 그 ID에 묶어 잠근다.
	sealer, err := sealerFor(userID)
	if err != nil {
		fmt.Println(err)
		return
	}
	entryID := uuid.Must(uuid.NewV7())
	place := crypto.AAD{Table: "diary_entries", Column: "body_enc", RowID: entryID}
	bodyEnc, err := sealer.SealString("오늘은 조금 걸었다", place)
	if err != nil {
		fmt.Println(err)
		return
	}

	// 읽기
	body, err := sealer.OpenString(bodyEnc, place)
	fmt.Println(body, err)

	// 다른 행에 가져다 놓으면 열리지 않는다.
	elsewhere := crypto.AAD{Table: "diary_entries", Column: "body_enc", RowID: uuid.Must(uuid.NewV7())}
	_, err = sealer.OpenString(bodyEnc, elsewhere)
	fmt.Println(errors.Is(err, crypto.ErrDecrypt))

	// 계정 삭제: 키 행을 지우고 캐시에서도 잊는다. 남은 암호문은 더는 열 수 없다.
	delete(keyTable, userID)
	cache.Forget(userID)
	_, err = sealerFor(userID)
	fmt.Println(errors.Is(err, errNoKeyRow))

	// Output:
	// 오늘은 조금 걸었다 <nil>
	// true
	// true
}
