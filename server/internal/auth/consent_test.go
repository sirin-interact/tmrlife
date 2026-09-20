package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/store"
)

func allCurrentExcept(skip string) []ConsentGrant {
	var out []ConsentGrant
	for _, c := range CurrentConsents() {
		if c.Kind != skip {
			out = append(out, c)
		}
	}
	return out
}

func TestCurrentConsents(t *testing.T) {
	t.Parallel()

	t.Run("네 가지 동의가 모두 필수이고 판이 적혀 있다", func(t *testing.T) {
		t.Parallel()
		current := CurrentConsents()
		kinds := make([]string, 0, len(current))
		for _, c := range current {
			kinds = append(kinds, c.Kind)
			assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, c.Version, "판은 문서가 바뀐 날짜로 적는다")
		}
		assert.ElementsMatch(t, []string{
			store.ConsentTerms, store.ConsentPrivacy, store.ConsentSensitiveData, store.ConsentOverseasTransfer,
		}, kinds)
	})

	t.Run("돌려받은 목록을 고쳐도 다음 호출에 번지지 않는다", func(t *testing.T) {
		t.Parallel()
		first := CurrentConsents()
		first[0].Version = "1999-01-01"
		assert.Equal(t, TermsVersion, CurrentConsents()[0].Version)
	})
}

func TestCheckConsents(t *testing.T) {
	t.Parallel()

	t.Run("넷 모두 지금 판이면 저장할 동의 넷을 돌려준다", func(t *testing.T) {
		t.Parallel()
		got, err := checkConsents(CurrentConsents())
		require.NoError(t, err)
		assert.Equal(t, CurrentConsents(), got)
	})

	t.Run("같은 동의가 두 번 와도 한 번만 저장한다", func(t *testing.T) {
		t.Parallel()
		given := append(CurrentConsents(), CurrentConsents()...)
		got, err := checkConsents(given)
		require.NoError(t, err)
		assert.Len(t, got, 4)
	})

	t.Run("옛 판과 지금 판이 함께 오면 지금 판으로 친다", func(t *testing.T) {
		t.Parallel()
		given := append(CurrentConsents(), ConsentGrant{Kind: store.ConsentTerms, Version: "2020-01-01"})
		_, err := checkConsents(given)
		require.NoError(t, err)
	})

	tests := []struct {
		name         string
		given        []ConsentGrant
		wantMissing  []string
		wantOutdated []string
		wantUnknown  int
	}{
		{
			name:  "아무 동의도 없으면 넷 모두 빠졌다고 알려준다",
			given: nil,
			wantMissing: []string{
				store.ConsentOverseasTransfer, store.ConsentPrivacy, store.ConsentSensitiveData, store.ConsentTerms,
			},
		},
		{
			name:        "마음 기록을 다루는 데 대한 동의가 빠지면 가입할 수 없다",
			given:       allCurrentExcept(store.ConsentSensitiveData),
			wantMissing: []string{store.ConsentSensitiveData},
		},
		{
			name:        "해외 전송에 대한 동의가 빠지면 가입할 수 없다",
			given:       allCurrentExcept(store.ConsentOverseasTransfer),
			wantMissing: []string{store.ConsentOverseasTransfer},
		},
		{
			name:         "옛 판에 동의한 것은 동의로 치지 않는다",
			given:        append(allCurrentExcept(store.ConsentPrivacy), ConsentGrant{Kind: store.ConsentPrivacy, Version: "2025-01-01"}),
			wantOutdated: []string{store.ConsentPrivacy},
		},
		{
			name:         "판을 비워 보낸 것도 지금 판이 아니다",
			given:        append(allCurrentExcept(store.ConsentTerms), ConsentGrant{Kind: store.ConsentTerms}),
			wantOutdated: []string{store.ConsentTerms},
		},
		{
			name:        "모르는 종류가 섞여 있으면 받지 않는다",
			given:       append(CurrentConsents(), ConsentGrant{Kind: "marketing", Version: "2026-09-20"}),
			wantUnknown: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := checkConsents(tt.given)
			require.ErrorIs(t, err, ErrConsentRequired)
			assert.Nil(t, got)

			var consentErr *ConsentError
			require.ErrorAs(t, err, &consentErr)
			assert.Equal(t, tt.wantMissing, consentErr.Missing)
			assert.Equal(t, tt.wantOutdated, consentErr.Outdated)
			assert.Equal(t, tt.wantUnknown, consentErr.Unknown)
			assert.NotContains(t, err.Error(), "marketing", "요청에서 온 글자를 오류 문구에 옮겨 적지 않는다")
		})
	}
}
