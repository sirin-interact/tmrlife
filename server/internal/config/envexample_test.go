package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envNames는 설정 틀의 태그에서 환경 변수 이름을 모은다.
func envNames(t *testing.T, frames ...any) []string {
	t.Helper()
	var names []string
	for _, frame := range frames {
		typ := reflect.TypeOf(frame)
		for i := range typ.NumField() {
			tag := typ.Field(i).Tag.Get("env")
			name, _, _ := strings.Cut(tag, ",")
			require.NotEmpty(t, name, "%s.%s: env 태그가 없다", typ.Name(), typ.Field(i).Name)
			names = append(names, name)
		}
	}
	return names
}

func TestEnvExampleListsEveryVariable(t *testing.T) {
	// 저장소 루트의 파일이다. 서버 모듈만 떼어 돌리는 곳에는 없을 수 있다.
	body, err := os.ReadFile(filepath.Join("..", "..", "..", ".env.example"))
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("저장소 루트의 .env.example이 없다")
	}
	require.NoError(t, err)

	// 값을 채워 둔 줄(NAME=...)과 주석으로 둔 줄(# NAME=...)을 모두 적은 것으로 친다.
	listed := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^#? ?([A-Z][A-Z0-9_]*)=`).FindAllStringSubmatch(string(body), -1) {
		listed[m[1]] = true
	}

	for _, name := range envNames(t, raw{}, rawMigration{}) {
		assert.True(t, listed[name], "%s: 설정이 읽는 변수인데 .env.example에 없다. 무엇을 정하는 값인지와 함께 적는다", name)
	}
}
