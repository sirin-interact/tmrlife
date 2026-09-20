package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errBrokenRandom = errors.New("broken random source")

// brokenReader는 난수원이 고장 난 상황을 만든다.
type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errBrokenRandom }

func TestNewToken(t *testing.T) {
	t.Parallel()

	t.Run("무작위 32바이트를 base64url로 적고, 저장할 것은 그 해시다", func(t *testing.T) {
		t.Parallel()
		token, hash, err := newToken(rand.Reader)
		require.NoError(t, err)

		assert.Len(t, token.Reveal(), 43)
		assert.NotContains(t, token.Reveal(), "=", "쿠키 값에 채움 글자를 넣지 않는다")

		raw, err := base64.RawURLEncoding.DecodeString(token.Reveal())
		require.NoError(t, err)
		require.Len(t, raw, 32)
		want := sha256.Sum256(raw)
		assert.Equal(t, want[:], hash)

		again, ok := hashToken(token.Reveal())
		require.True(t, ok)
		assert.Equal(t, hash, again)
	})

	t.Run("뽑을 때마다 다른 토큰이 나온다", func(t *testing.T) {
		t.Parallel()
		seen := map[string]bool{}
		for range 100 {
			token, _, err := newToken(rand.Reader)
			require.NoError(t, err)
			require.False(t, seen[token.Reveal()])
			seen[token.Reveal()] = true
		}
	})

	t.Run("난수원이 고장 나면 토큰을 만들지 않는다", func(t *testing.T) {
		t.Parallel()
		token, hash, err := newToken(brokenReader{})
		require.ErrorIs(t, err, errBrokenRandom)
		assert.True(t, token.IsZero())
		assert.Nil(t, hash)
	})
}

func TestHashToken_RejectsMalformed(t *testing.T) {
	t.Parallel()

	valid, _, err := newToken(rand.Reader)
	require.NoError(t, err)
	// 마지막 글자는 4비트만 쓴다. 남는 비트를 채운 글자는 같은 바이트로 풀리지만 받지 않는다.
	nonCanonical := valid.Reveal()[:42] + "B"
	if strings.HasSuffix(valid.Reveal(), "B") {
		nonCanonical = valid.Reveal()[:42] + "C"
	}

	tests := []struct {
		name  string
		token string
	}{
		{"빈 값", ""},
		{"너무 짧다", "abc"},
		{"한 글자 모자란다", valid.Reveal()[:42]},
		{"한 글자 남는다", valid.Reveal() + "A"},
		{"채움 글자가 붙었다", valid.Reveal()[:42] + "="},
		{"base64url에 없는 글자다", strings.Repeat("+", 43)},
		{"같은 바이트로 풀리는 다른 글자다", nonCanonical},
		{"아주 긴 값", strings.Repeat("A", 1<<20)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hash, ok := hashToken(tt.token)
			assert.False(t, ok)
			assert.Nil(t, hash)
		})
	}
}

func TestSessionToken_NeverPrintsItself(t *testing.T) {
	t.Parallel()

	token, _, err := newToken(rand.Reader)
	require.NoError(t, err)
	secret := token.Reveal()
	issued := IssuedSession{Token: token}

	t.Run("fmt의 어떤 서식으로 찍어도 나오지 않는다", func(t *testing.T) {
		t.Parallel()
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
			assert.NotContains(t, fmt.Sprintf(verb, token), secret)
			assert.NotContains(t, fmt.Sprintf(verb, issued), secret)
			assert.NotContains(t, fmt.Sprintf(verb, &issued), secret)
		}
		assert.NotContains(t, token.String(), secret)
	})

	t.Run("slog로 넘겨도 나오지 않는다", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, nil))
		logger.Info("session issued", "session_token", token, "issued", issued)
		assert.NotContains(t, buf.String(), secret)
		assert.Contains(t, buf.String(), redactedMarker)
	})

	t.Run("JSON으로 직렬화해도 나오지 않는다", func(t *testing.T) {
		t.Parallel()
		out, err := json.Marshal(issued)
		require.NoError(t, err)
		assert.NotContains(t, string(out), secret)
	})
}
