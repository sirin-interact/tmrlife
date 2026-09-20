package crypto

import (
	"bytes"
	"errors"
	"testing"
)

// 퍼즈 시험은 고정된 키와 고정된 암호문을 쓴다.
// 키를 매번 새로 뽑으면 찾아낸 실패를 저장된 입력으로 다시 일으킬 수 없다.

func FuzzSealerOpen(f *testing.F) {
	sealer := goldenSealer(f)
	valid := mustHex(f, goldenSealedHex)

	f.Add(valid, goldenTable, goldenColumn)
	f.Add(valid[:len(valid)-1], goldenTable, goldenColumn)
	f.Add(valid[:minSealedSize], goldenTable, goldenColumn)
	f.Add(valid[:headerSize], goldenTable, goldenColumn)
	f.Add(append([]byte{2}, valid[1:]...), goldenTable, goldenColumn)
	f.Add([]byte{}, goldenTable, goldenColumn)
	f.Add([]byte{formatV1}, "", "")
	f.Add(valid, "memories", goldenColumn)
	f.Add(valid, goldenTable, "오늘은 조금 걸었다")

	f.Fuzz(func(t *testing.T, sealed []byte, table, column string) {
		opened, err := sealer.Open(sealed, AAD{Table: table, Column: column, RowID: testRowID})
		if err != nil {
			if !errors.Is(err, ErrFormat) && !errors.Is(err, ErrDecrypt) && !errors.Is(err, ErrInvalidAAD) {
				t.Fatalf("정해 둔 오류 가운데 하나가 아니다: %T", err)
			}
			if opened != nil {
				t.Fatal("실패했는데 값이 함께 나왔다")
			}
			return
		}
		// 키를 모르고 만든 입력이 열렸다면 위조에 성공한 것이다. 열려도 되는 입력은 하나뿐이다.
		if !bytes.Equal(sealed, valid) || table != goldenTable || column != goldenColumn {
			t.Fatalf("만든 적 없는 암호문이 열렸다: %d bytes", len(sealed))
		}
		if string(opened) != goldenPlaintext {
			t.Fatal("열린 값이 잠근 값과 다르다")
		}
	})
}

func FuzzKeyRingUnwrap(f *testing.F) {
	ring, err := NewKeyRing(goldenKEKVersion, map[int][]byte{goldenKEKVersion: mustHex(f, goldenKEKHex), 1: filledKey(0x11)})
	if err != nil {
		f.Fatal(err)
	}
	valid := mustHex(f, goldenWrappedHex)

	f.Add(valid, goldenKEKVersion)
	f.Add(valid, 1)
	f.Add(valid, 0)
	f.Add(valid, -1)
	f.Add(valid[:len(valid)-1], goldenKEKVersion)
	f.Add(append([]byte{0}, valid[1:]...), goldenKEKVersion)
	f.Add([]byte{}, goldenKEKVersion)

	f.Fuzz(func(t *testing.T, wrapped []byte, kekVersion int) {
		sealer, err := ring.Unwrap(testUserID, wrapped, kekVersion)
		if err != nil {
			if !errors.Is(err, ErrFormat) && !errors.Is(err, ErrDecrypt) && !errors.Is(err, ErrKeyVersion) {
				t.Fatalf("정해 둔 오류 가운데 하나가 아니다: %T", err)
			}
			if sealer != nil {
				t.Fatal("실패했는데 Sealer가 함께 나왔다")
			}
			return
		}
		if !bytes.Equal(wrapped, valid) || kekVersion != goldenKEKVersion {
			t.Fatalf("만든 적 없는 키 행이 풀렸다: %d bytes", len(wrapped))
		}
	})
}
