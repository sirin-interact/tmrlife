package migrations

import (
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fileNamePattern = regexp.MustCompile(`^(\d{5})_[a-z0-9_]+\.sql$`)

func TestEmbeddedMigrations(t *testing.T) {
	entries, err := fs.ReadDir(FS, ".")
	require.NoError(t, err)
	require.NotEmpty(t, entries, "마이그레이션이 실행 파일에 담겨 있어야 한다")

	t.Run("첫 마이그레이션은 확장만 만든다", func(t *testing.T) {
		body, err := fs.ReadFile(FS, "00001_extensions.sql")
		require.NoError(t, err)
		assert.Contains(t, string(body), "CREATE EXTENSION IF NOT EXISTS citext")
		assert.NotContains(t, strings.ToUpper(string(body)), "CREATE TABLE")
	})

	t.Run("파일 이름이 규칙을 따르고 번호가 빠짐없이 이어진다", func(t *testing.T) {
		// 나란히 작업하다 같은 번호를 두 번 쓰거나 번호를 건너뛰면 적용 순서가 환경마다 달라진다.
		seen := map[int]string{}
		for _, e := range entries {
			m := fileNamePattern.FindStringSubmatch(e.Name())
			require.NotNil(t, m, "%s: 00001_이름.sql 꼴이어야 한다", e.Name())
			n, err := strconv.Atoi(m[1])
			require.NoError(t, err)
			if prev, dup := seen[n]; dup {
				t.Fatalf("번호 %05d가 %s와 %s에서 겹친다", n, prev, e.Name())
			}
			seen[n] = e.Name()
		}
		for n := 1; n <= len(seen); n++ {
			assert.Contains(t, seen, n, "번호 %05d가 빠졌다", n)
		}
	})

	t.Run("모든 파일에 올리는 구문과 내리는 구문이 있다", func(t *testing.T) {
		for _, e := range entries {
			body, err := fs.ReadFile(FS, e.Name())
			require.NoError(t, err)
			assert.Contains(t, string(body), "-- +goose Up", e.Name())
			assert.Contains(t, string(body), "-- +goose Down", e.Name())
		}
	})
}
