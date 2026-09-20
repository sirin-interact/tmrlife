package crypto

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSealer_RoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		plaintext []byte
	}{
		{"빈 값", []byte{}},
		{"nil", nil},
		{"한 바이트", []byte{0x00}},
		{"한국어 문장", []byte(written)},
		{"0바이트가 섞인 값", []byte("앞\x00뒤\x00")},
		{"GCM 블록 경계에 딱 맞는 길이", bytes.Repeat([]byte{0x7f}, 32)},
		{"1MiB", bytes.Repeat([]byte("가나다라"), 1<<20/12)},
	}
	sealer := newTestSealer(t, newTestRing(t), testUserID)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sealed, err := sealer.Seal(tt.plaintext, diaryAAD())
			require.NoError(t, err)

			assert.Equal(t, formatV1, sealed[0], "첫 바이트는 판 번호다")
			assert.Len(t, sealed, headerSize+len(tt.plaintext)+tagSize)
			if len(tt.plaintext) >= 8 {
				assert.NotContains(t, string(sealed), string(tt.plaintext[:8]), "평문이 그대로 보이면 안 된다")
			}

			opened, err := sealer.Open(sealed, diaryAAD())
			require.NoError(t, err)
			require.NotNil(t, opened, "빈 값도 nil이 아닌 슬라이스로 돌아와야 호출한 쪽이 구분하지 않아도 된다")
			assert.Equal(t, string(tt.plaintext), string(opened))
		})
	}
}

func TestSealer_StringRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		plaintext string
	}{
		{"빈 문자열", ""},
		{"한국어 문장", written},
		{"줄바꿈과 이모지", "첫 줄\n둘째 줄 🌙"},
	}
	sealer := newTestSealer(t, newTestRing(t), testUserID)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sealed, err := sealer.SealString(tt.plaintext, diaryAAD())
			require.NoError(t, err)
			opened, err := sealer.OpenString(sealed, diaryAAD())
			require.NoError(t, err)
			assert.Equal(t, tt.plaintext, opened)
		})
	}

	t.Run("열지 못하면 빈 문자열과 오류가 나온다", func(t *testing.T) {
		sealed, err := sealer.SealString(written, diaryAAD())
		require.NoError(t, err)
		opened, err := sealer.OpenString(flipped(sealed, len(sealed)-1, 0x01), diaryAAD())
		require.ErrorIs(t, err, ErrDecrypt)
		assert.Empty(t, opened)
	})
}

func TestSealer_Open_Tampered(t *testing.T) {
	sealer := newTestSealer(t, newTestRing(t), testUserID)
	sealed, err := sealer.SealString(written, diaryAAD())
	require.NoError(t, err)

	regions := []struct {
		name       string
		start, end int
		wantErr    error
	}{
		{"판 번호를 바꾸면 꼴이 틀렸다고 한다", 0, 1, ErrFormat},
		{"nonce를 바꾸면 열리지 않는다", 1, headerSize, ErrDecrypt},
		{"본문을 바꾸면 열리지 않는다", headerSize, len(sealed) - tagSize, ErrDecrypt},
		{"인증 태그를 바꾸면 열리지 않는다", len(sealed) - tagSize, len(sealed), ErrDecrypt},
	}
	for _, region := range regions {
		t.Run(region.name, func(t *testing.T) {
			require.Less(t, region.start, region.end)
			for index := region.start; index < region.end; index++ {
				for _, mask := range []byte{0x01, 0x80, 0xff} {
					opened, err := sealer.Open(flipped(sealed, index, mask), diaryAAD())
					require.ErrorIs(t, err, region.wantErr, "index=%d mask=%#x", index, mask)
					require.Nil(t, opened)
				}
			}
		})
	}

	t.Run("손대지 않은 원본은 여전히 열린다", func(t *testing.T) {
		opened, err := sealer.OpenString(sealed, diaryAAD())
		require.NoError(t, err)
		assert.Equal(t, written, opened)
	})
}

func TestSealer_Open_WrongPlace(t *testing.T) {
	tests := []struct {
		name     string
		sealedAt AAD
		openedAt AAD
	}{
		{
			"다른 테이블로 옮기면 열리지 않는다",
			diaryAAD(),
			AAD{Table: "memories", Column: "body_enc", RowID: testRowID},
		},
		{
			"다른 컬럼으로 옮기면 열리지 않는다",
			diaryAAD(),
			AAD{Table: "diary_entries", Column: "title_enc", RowID: testRowID},
		},
		{
			"다른 행으로 옮기면 열리지 않는다",
			diaryAAD(),
			AAD{Table: "diary_entries", Column: "body_enc", RowID: otherRowID},
		},
		{
			"테이블과 컬럼 이름을 맞바꾸면 열리지 않는다",
			AAD{Table: "alpha", Column: "beta", RowID: testRowID},
			AAD{Table: "beta", Column: "alpha", RowID: testRowID},
		},
		{
			"두 이름을 이어 붙인 글자가 같아도 경계가 다르면 열리지 않는다",
			AAD{Table: "ab", Column: "c", RowID: testRowID},
			AAD{Table: "a", Column: "bc", RowID: testRowID},
		},
	}
	sealer := newTestSealer(t, newTestRing(t), testUserID)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sealed, err := sealer.SealString(written, tt.sealedAt)
			require.NoError(t, err)

			opened, err := sealer.Open(sealed, tt.openedAt)
			require.ErrorIs(t, err, ErrDecrypt)
			require.Nil(t, opened)

			opened, err = sealer.Open(sealed, tt.sealedAt)
			require.NoError(t, err, "제자리에서는 열려야 한다")
			assert.Equal(t, written, string(opened))
		})
	}
}

func TestSealer_Open_WrongUserKey(t *testing.T) {
	ring := newTestRing(t)
	mine := newTestSealer(t, ring, testUserID)
	theirs := newTestSealer(t, ring, otherUserID)

	sealed, err := mine.SealString(written, diaryAAD())
	require.NoError(t, err)

	t.Run("다른 사용자의 키로는 열리지 않는다", func(t *testing.T) {
		opened, err := theirs.Open(sealed, diaryAAD())
		require.ErrorIs(t, err, ErrDecrypt)
		assert.Nil(t, opened)
	})

	t.Run("같은 사용자라도 새로 만든 키로는 열리지 않는다", func(t *testing.T) {
		regenerated := newTestSealer(t, ring, testUserID)
		opened, err := regenerated.Open(sealed, diaryAAD())
		require.ErrorIs(t, err, ErrDecrypt)
		assert.Nil(t, opened)
	})
}

func TestSealer_Open_Truncated(t *testing.T) {
	sealer := newTestSealer(t, newTestRing(t), testUserID)
	sealed, err := sealer.SealString(written, diaryAAD())
	require.NoError(t, err)

	t.Run("어디서 잘려도 열리지 않는다", func(t *testing.T) {
		for length := range len(sealed) {
			opened, err := sealer.Open(sealed[:length], diaryAAD())
			require.Nil(t, opened, "length=%d", length)
			if length < minSealedSize {
				require.ErrorIs(t, err, ErrFormat, "length=%d", length)
			} else {
				require.ErrorIs(t, err, ErrDecrypt, "length=%d", length)
			}
		}
	})

	t.Run("nil은 꼴이 틀린 것으로 본다", func(t *testing.T) {
		_, err := sealer.Open(nil, diaryAAD())
		require.ErrorIs(t, err, ErrFormat)
	})

	t.Run("뒤에 바이트가 붙어도 열리지 않는다", func(t *testing.T) {
		_, err := sealer.Open(append(bytes.Clone(sealed), 0x00), diaryAAD())
		require.ErrorIs(t, err, ErrDecrypt)
	})
}

func TestSealer_Open_UnknownFormatVersion(t *testing.T) {
	sealer := newTestSealer(t, newTestRing(t), testUserID)
	sealed, err := sealer.SealString(written, diaryAAD())
	require.NoError(t, err)

	for _, version := range []byte{0, 2, 0x7f, 0xff} {
		t.Run(fmt.Sprintf("판 번호 %d는 거부한다", version), func(t *testing.T) {
			forged := bytes.Clone(sealed)
			forged[0] = version
			_, err := sealer.Open(forged, diaryAAD())
			require.ErrorIs(t, err, ErrFormat)
			assert.NotErrorIs(t, err, ErrDecrypt, "열어 보기 전에 거부해야 한다")
		})
	}
}

func TestSealer_InvalidAAD(t *testing.T) {
	tests := []struct {
		name string
		aad  AAD
	}{
		{"테이블 이름이 비었다", AAD{Table: "", Column: "body_enc", RowID: testRowID}},
		{"컬럼 이름이 비었다", AAD{Table: "diary_entries", Column: "", RowID: testRowID}},
		{"행 ID를 만들지 않았다", AAD{Table: "diary_entries", Column: "body_enc", RowID: uuid.Nil}},
		{"대문자가 섞였다", AAD{Table: "DiaryEntries", Column: "body_enc", RowID: testRowID}},
		{"숫자로 시작한다", AAD{Table: "1table", Column: "body_enc", RowID: testRowID}},
		{"식별자 길이 한도를 넘었다", AAD{Table: strings.Repeat("a", maxIdentifierLen+1), Column: "body_enc", RowID: testRowID}},
		{"이름 자리에 사용자의 글이 들어왔다", AAD{Table: "diary_entries", Column: written, RowID: testRowID}},
	}
	sealer := newTestSealer(t, newTestRing(t), testUserID)
	valid, err := sealer.SealString(written, diaryAAD())
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sealed, err := sealer.SealString(written, tt.aad)
			require.ErrorIs(t, err, ErrInvalidAAD)
			assert.Nil(t, sealed)
			assert.NotContains(t, err.Error(), written, "잘못 들어온 값을 오류에 옮겨 적으면 안 된다")

			opened, err := sealer.Open(valid, tt.aad)
			require.ErrorIs(t, err, ErrInvalidAAD)
			assert.Nil(t, opened)
			assert.NotContains(t, err.Error(), written)
		})
	}

	t.Run("한도에 딱 맞는 이름과 숫자가 섞인 이름은 받는다", func(t *testing.T) {
		aad := AAD{Table: strings.Repeat("a", maxIdentifierLen), Column: "_phq8_item_2_enc", RowID: testRowID}
		sealed, err := sealer.SealString(written, aad)
		require.NoError(t, err)
		opened, err := sealer.OpenString(sealed, aad)
		require.NoError(t, err)
		assert.Equal(t, written, opened)
	})
}

func TestSealer_NonceNeverRepeats(t *testing.T) {
	const seals = 20000
	sealer := newTestSealer(t, newTestRing(t), testUserID)

	nonces := make(map[[nonceSize]byte]struct{}, seals)
	for range seals {
		sealed, err := sealer.SealString(written, diaryAAD())
		require.NoError(t, err)
		var nonce [nonceSize]byte
		copy(nonce[:], sealed[1:headerSize])
		nonces[nonce] = struct{}{}
	}
	assert.Len(t, nonces, seals, "같은 글을 같은 자리에 잠가도 nonce는 매번 달라야 한다")
}

func TestSealer_BrokenRandomSource(t *testing.T) {
	tests := []struct {
		name    string
		random  io.Reader
		wantErr error
	}{
		{"난수원이 오류를 내면 잠그지 않는다", brokenReader{}, errBrokenRandom},
		{"난수가 nonce 길이에 못 미치면 잠그지 않는다", bytes.NewReader(make([]byte, nonceSize-1)), io.ErrUnexpectedEOF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sealer := newTestSealer(t, newTestRing(t), testUserID)
			sealer.random = tt.random

			sealed, err := sealer.SealString(written, diaryAAD())
			require.ErrorIs(t, err, tt.wantErr)
			assert.Nil(t, sealed, "덜 채워진 nonce로 잠근 결과가 나가면 안 된다")
		})
	}
}

func TestSealer_WithoutKey(t *testing.T) {
	tests := []struct {
		name   string
		sealer *Sealer
	}{
		{"nil Sealer", nil},
		{"빈 Sealer", &Sealer{}},
	}
	for _, tt := range tests {
		t.Run(tt.name+"는 패닉 대신 오류를 낸다", func(t *testing.T) {
			_, err := tt.sealer.Seal([]byte(written), diaryAAD())
			require.ErrorIs(t, err, ErrInvalidKey)
			_, err = tt.sealer.Open([]byte{formatV1}, diaryAAD())
			require.ErrorIs(t, err, ErrInvalidKey)
		})
	}

	t.Run("0으로만 찬 데이터 키로는 Sealer를 만들지 않는다", func(t *testing.T) {
		_, err := newSealer(make([]byte, keySize))
		require.ErrorIs(t, err, ErrInvalidKey)
	})
}

func TestSealer_ConcurrentUse(t *testing.T) {
	const (
		workers    = 16
		iterations = 200
	)
	sealer := newTestSealer(t, newTestRing(t), testUserID)

	failures := make(chan error, workers)
	var wg sync.WaitGroup
	for worker := range workers {
		wg.Go(func() {
			for i := range iterations {
				text := fmt.Sprintf("%s %d-%d", written, worker, i)
				aad := AAD{Table: "utterances", Column: "text_enc", RowID: uuid.New()}
				sealed, err := sealer.SealString(text, aad)
				if err != nil {
					failures <- fmt.Errorf("seal: %w", err)
					return
				}
				opened, err := sealer.OpenString(sealed, aad)
				if err != nil {
					failures <- fmt.Errorf("open: %w", err)
					return
				}
				if opened != text {
					failures <- fmt.Errorf("worker %d iteration %d opened another text", worker, i)
					return
				}
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
}

func TestSealer_ErrorsCarryNoSecrets(t *testing.T) {
	ring := newTestRing(t)
	sealer := newTestSealer(t, ring, testUserID)
	sealed, err := sealer.SealString(written, diaryAAD())
	require.NoError(t, err)

	t.Run("열지 못한 오류에는 자리만 적힌다", func(t *testing.T) {
		_, err := newTestSealer(t, ring, otherUserID).Open(sealed, diaryAAD())
		require.ErrorIs(t, err, ErrDecrypt)
		assert.Contains(t, err.Error(), "diary_entries.body_enc")
		assert.Contains(t, err.Error(), testRowID.String())
		assert.NotContains(t, err.Error(), written)
		assert.NotContains(t, err.Error(), "cipher:", "표준 라이브러리의 오류 문구를 그대로 내보내지 않는다")
	})

	t.Run("Sealer를 출력해도 고정 문자열만 나온다", func(t *testing.T) {
		assert.Equal(t, "[REDACTED] [REDACTED] [REDACTED]", fmt.Sprintf("%v %+v %s", sealer, sealer, sealer))

		var buf bytes.Buffer
		slog.New(slog.NewJSONHandler(&buf, nil)).Info("sealer", "value", sealer)
		assert.Contains(t, buf.String(), `"value":"[REDACTED]"`)
	})
}
